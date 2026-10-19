package restart

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/gather"
)

const (
	// termGrace is how long a process gets to exit on SIGTERM before SIGKILL.
	termGrace = 5 * time.Second
	// killGrace is how long the kernel gets to reap a SIGKILLed process.
	killGrace = 2 * time.Second
	// pollEvery is the cadence every bounded wait in this package polls at.
	pollEvery = 100 * time.Millisecond
)

// Signaler delivers sig to pid; signal 0 only asks whether pid exists.
// syscall.Kill is the real one; a test's fake plays the kernel.
type Signaler func(pid int, sig syscall.Signal) error

// process is one row of the process table, with the identity a stop
// re-checks before it signals: the same pid must still run the same argv
// since the same start time.
type process struct {
	PID    int
	Parent int
	Argv   []string
	Start  uint64
}

// command is the process's argv as one line.
func (proc process) command() string { return strings.Join(proc.Argv, " ") }

// processTable is one read of the process table: every process it could
// read, how many pids it listed (so an empty match proves the enumerator
// ran), and every pid of this user it could not read, with why.
type processTable struct {
	Processes  []process
	Listed     int
	Unreadable []string
}

// unreadableErr names the pids the table could not read, or nil: any of them
// may be a process a family stops, so the look is failed, never "nothing".
func (table processTable) unreadableErr() error {
	if len(table.Unreadable) == 0 {
		return nil
	}
	return fmt.Errorf("%d process(es) of this user could not be read, so they may run unstopped: %s",
		len(table.Unreadable), strings.Join(table.Unreadable, "; "))
}

// readTable lists every process the table can read. A process that exits
// mid-read, a zombie and another user's process (its argv protected, its uid
// proved foreign) are skipped; any other unreadable pid is named in
// Unreadable, and the listing itself failing is an error.
func (finder *discoverer) readTable() (processTable, error) {
	if finder.deps.Procs == nil {
		return processTable{}, errors.New("no process table to read")
	}
	pids, err := finder.deps.Procs.PIDs()
	if err != nil {
		return processTable{}, fmt.Errorf("list processes: %w", err)
	}
	table := processTable{Listed: len(pids)}
	for _, pid := range pids {
		if pid == finder.deps.Self {
			continue
		}
		argv, argvErr := finder.deps.Procs.Cmdline(pid)
		stat, statErr := finder.deps.Procs.Stat(pid)
		if readErr := errors.Join(argvErr, statErr); readErr != nil {
			if !finder.skippable(pid, readErr) {
				table.Unreadable = append(table.Unreadable, fmt.Sprintf("pid %d: %s", pid, stepLine(readErr.Error())))
			}
			continue
		}
		if len(argv) == 0 {
			continue // a kernel thread or a zombie: no command to match
		}
		table.Processes = append(table.Processes, process{
			PID: pid, Parent: stat.ParentPID, Argv: argv, Start: stat.StartTime,
		})
	}
	return table, nil
}

// skippable reports whether a pid the table could not read is provably none
// of this restart's business: gone, a zombie, or another user's process.
func (finder *discoverer) skippable(pid int, readErr error) bool {
	if errors.Is(readErr, fs.ErrNotExist) || errors.Is(readErr, syscall.ESRCH) {
		return true
	}
	if finder.deps.Signal != nil && errors.Is(finder.deps.Signal(pid, 0), syscall.ESRCH) {
		return true
	}
	identities, ok := finder.deps.Procs.(gather.ProcIdentity)
	if !ok {
		return false
	}
	identity, err := identities.ProcessIdentity(pid)
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	return err == nil && (identity.Zombie || identity.EffectiveUID != finder.deps.EffectiveUID)
}

// pfmArgs returns a pfm process's arguments after its global --config flag,
// and whether argv is a pfm process at all: argv[0] names a file called pfm,
// or this very binary's file name (a dev build is not called pfm).
func (finder *discoverer) pfmArgs(argv []string) ([]string, bool) {
	base := filepath.Base(argv[0])
	if base != "pfm" && (finder.deps.Executable == "" || base != filepath.Base(finder.deps.Executable)) {
		return nil, false
	}
	args := argv[1:]
	switch {
	case len(args) >= 2 && args[0] == "--config":
		args = args[2:]
	case len(args) >= 1 && strings.HasPrefix(args[0], "--config="):
		args = args[1:]
	}
	return args, true
}

// helperKind names the per-chat helper argv is, or "" for none: a chat's cost
// sink, its stdio MCP server, or a harvester Python sidecar (the converter or
// browser worker, each run from the managed runtime's project directory).
func (finder *discoverer) helperKind(argv []string) string {
	if args, ok := finder.pfmArgs(argv); ok {
		switch {
		case len(args) >= 2 && args[0] == "internal" && args[1] == "cost-sink":
			return "cost sink"
		case len(args) >= 3 && args[0] == "mcp" && args[1] == "serve" && args[2] == "--stdio":
			return "stdio MCP server"
		}
		return ""
	}
	if len(argv) >= 2 && filepath.Base(filepath.Dir(argv[1])) == "project" &&
		(filepath.Base(argv[1]) == "converter.py" || filepath.Base(argv[1]) == "browser.py") {
		return "harvester sidecar " + filepath.Base(argv[1])
	}
	return ""
}

// mcpDaemon reports whether argv is the loopback MCP daemon, `pfm mcp serve`.
func (finder *discoverer) mcpDaemon(argv []string) bool {
	args, ok := finder.pfmArgs(argv)
	return ok && len(args) >= 2 && args[0] == "mcp" && args[1] == "serve" && !slices.Contains(args, "--stdio")
}

// processUnit is the stop of one process: SIGTERM, then SIGKILL, each
// confirmed against the table.
func (finder *discoverer) processUnit(family, kind string, proc process) Unit {
	stopper := processStopper{procs: finder.deps.Procs, signal: finder.deps.Signal, clock: finder.deps.Clock}
	return Unit{
		Family:  family,
		Name:    fmt.Sprintf("%s pid %d", kind, proc.PID),
		FoundBy: "process table: " + clipArgv(proc.command()),
		StopHow: fmt.Sprintf("SIGTERM, SIGKILL after %s; verified gone from the process table", termGrace),
		Stop:    func(ctx context.Context) (string, error) { return stopper.stop(ctx, proc) },
	}
}

// clipArgv keeps a long argv readable on its plan line.
func clipArgv(command string) string {
	const limit = 160
	if len(command) <= limit {
		return command
	}
	return command[:limit] + "…"
}

// processStopper stops one process and proves it gone.
type processStopper struct {
	procs  gather.ProcFS
	signal Signaler
	clock  clock.Clock
}

// stop signals proc only while the pid still runs the same command since the
// same start, so a recycled pid is never hit.
func (stopper processStopper) stop(ctx context.Context, proc process) (string, error) {
	same, err := stopper.same(proc)
	if err != nil {
		return "", err
	}
	if !same {
		return "already gone", nil
	}
	if err := stopper.signal(proc.PID, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return "already gone", nil
		}
		return "", fmt.Errorf("SIGTERM pid %d: %w", proc.PID, err)
	}
	gone, err := stopper.waitGone(ctx, proc, termGrace)
	if err != nil {
		return "", err
	}
	if gone {
		return "exited on SIGTERM", nil
	}
	if err := stopper.signal(proc.PID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return "", fmt.Errorf("SIGKILL pid %d after %s: %w", proc.PID, termGrace, err)
	}
	gone, err = stopper.waitGone(ctx, proc, killGrace)
	if err != nil {
		return "", err
	}
	if !gone {
		return "", fmt.Errorf("pid %d still runs %s after SIGKILL", proc.PID, clipArgv(proc.command()))
	}
	return "killed (SIGKILL after " + termGrace.String() + ")", nil
}

// same reports whether proc's pid still runs proc's command since proc's
// start. A pid the kernel no longer has is not the same; a table that cannot
// be read for a pid that still exists is an error, never "gone".
func (stopper processStopper) same(proc process) (bool, error) {
	stat, statErr := stopper.procs.Stat(proc.PID)
	argv, argvErr := stopper.procs.Cmdline(proc.PID)
	if statErr == nil && argvErr == nil {
		return stat.StartTime == proc.Start && slices.Equal(argv, proc.Argv), nil
	}
	if err := stopper.signal(proc.PID, 0); errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	return false, fmt.Errorf("read pid %d from the process table: %w", proc.PID, errors.Join(statErr, argvErr))
}

// waitGone polls until proc is gone or within elapses.
func (stopper processStopper) waitGone(ctx context.Context, proc process, within time.Duration) (bool, error) {
	deadline := stopper.clock.Now().Add(within)
	for {
		same, err := stopper.same(proc)
		if err != nil {
			return false, err
		}
		if !same {
			return true, nil
		}
		if !stopper.clock.Now().Before(deadline) {
			return false, nil
		}
		if err := stopper.clock.Sleep(ctx, pollEvery); err != nil {
			return false, fmt.Errorf("wait for pid %d to exit: %w", proc.PID, err)
		}
	}
}

// findHelpers is the chat-helpers family: every cost sink, stdio MCP server
// and harvester sidecar in the process table.
func (finder *discoverer) findHelpers(table processTable) {
	count := 0
	for _, proc := range table.Processes {
		kind := finder.helperKind(proc.Argv)
		if kind == "" || finder.claimed[proc.PID] {
			continue
		}
		finder.claimed[proc.PID] = true
		finder.plan.Units = append(finder.plan.Units, finder.processUnit(FamilyHelpers, kind, proc))
		count++
	}
	finder.found(FamilyHelpers, fmt.Sprintf(
		"%d helper process(es) of %d listed — process table: pfm internal cost-sink, pfm mcp serve --stdio, "+
			"harvester project/converter.py and project/browser.py", count, table.Listed))
	finder.failedOn(FamilyHelpers, table.unreadableErr())
}

// failedOn records err as family's failed find when there is one.
func (finder *discoverer) failedOn(family string, err error) {
	if err != nil {
		finder.failed(family, err)
	}
}

// findPFM is the pfm family: every pfm process no earlier family claimed,
// except this worker and the front that launched it.
func (finder *discoverer) findPFM(table processTable) {
	count := 0
	for _, proc := range table.Processes {
		if _, ok := finder.pfmArgs(
			proc.Argv,
		); !ok || finder.claimed[proc.PID] ||
			slices.Contains(finder.deps.Spare, proc.PID) {
			continue
		}
		finder.claimed[proc.PID] = true
		finder.plan.Units = append(finder.plan.Units, finder.processUnit(FamilyPFM, "pfm process", proc))
		count++
	}
	last := "this worker (pid " + strconv.Itoa(finder.deps.Self) + ") is last"
	if finder.deps.DryRun {
		last = "the restart worker is last"
	}
	finder.found(FamilyPFM, fmt.Sprintf(
		"%d other pfm process(es) of %d listed — process table, argv[0] named pfm; %s", count, table.Listed, last))
	finder.failedOn(FamilyPFM, table.unreadableErr())
}
