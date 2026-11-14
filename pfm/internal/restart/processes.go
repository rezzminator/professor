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

	"github.com/rezzminator/professor/pfm/internal/gather"
)

const (
	mcpCommand      = "mcp"
	mcpServeCommand = "serve"
	// termGrace is how long a process gets to exit on SIGTERM before SIGKILL.
	termGrace = 5 * time.Second
	// killGrace is how long the kernel gets to reap a SIGKILLed process.
	killGrace = 2 * time.Second
	// pollEvery is the cadence every bounded wait in this package polls at.
	pollEvery = 100 * time.Millisecond
)

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
	if finder.deps.Probe != nil && errors.Is(finder.deps.Probe(pid), syscall.ESRCH) {
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
// sink or its stdio MCP server.
func (finder *discoverer) helperKind(argv []string) string {
	if args, ok := finder.pfmArgs(argv); ok {
		switch {
		case len(args) >= 2 && args[0] == "internal" && args[1] == "cost-sink":
			return "cost sink"
		case len(args) >= 3 && args[0] == mcpCommand && args[1] == mcpServeCommand && args[2] == "--stdio":
			return "stdio MCP server"
		}
		return ""
	}
	return ""
}

// mcpDaemon reports whether argv is the loopback MCP daemon, `pfm mcp serve`.
func (finder *discoverer) mcpDaemon(argv []string) bool {
	args, ok := finder.pfmArgs(argv)
	return ok && len(args) >= 2 && args[0] == mcpCommand && args[1] == mcpServeCommand &&
		!slices.Contains(args, "--stdio")
}

type daemonOwnership uint8

const (
	daemonForeign daemonOwnership = iota
	daemonOwned
	daemonGone
)

func (finder *discoverer) daemonIdentityState(proc process) (daemonOwnership, error) {
	identities, ok := finder.deps.Procs.(gather.ProcIdentity)
	if !ok {
		return daemonForeign, fmt.Errorf("pid %d daemon identity reader unavailable", proc.PID)
	}
	identity, err := identities.ProcessIdentity(proc.PID)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
		return daemonGone, nil
	}
	if err != nil {
		return daemonForeign, fmt.Errorf("pid %d daemon identity: %w", proc.PID, err)
	}
	if identity.Zombie {
		return daemonGone, nil
	}
	if identity.EffectiveUID != finder.deps.EffectiveUID {
		return daemonForeign, nil
	}
	return daemonOwned, nil
}

// processUnit is the stop of one process: SIGTERM, then SIGKILL, each
// confirmed against the table.
func (finder *discoverer) processUnit(family, kind string, proc process) Unit {
	stopper := finder.deps.stopper(&finder.deps.Install)
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

// processStopper stops one process through the signal door and proves it gone.
type processStopper struct {
	door  gather.Door
	probe gather.Probe
	// verify is the site's own match, held before the first signal.
	verify func(process) (bool, error)
	// owner, when set, is the install that must have launched the process,
	// checked by the door right before each signal; nil stops a process by its
	// identity alone (one this install did not launch, like a Herdr pane's).
	owner             *gather.Install
	ownershipVerified bool
}

// stopper is a processStopper over these dependencies; owner as processStopper's.
func (dependencies Deps) stopper(owner *gather.Install) processStopper {
	return processStopper{door: dependencies.door(), probe: dependencies.Probe, owner: owner}
}

// stop signals proc only while the pid still runs the same command since the
// same start, so a recycled pid is never hit: the door re-reads the process
// right before SIGTERM and again before SIGKILL.
func (stopper processStopper) stop(ctx context.Context, proc process) (string, error) {
	same, err := stopper.same(proc)
	if err != nil {
		return "", err
	}
	if !same {
		return "already gone", nil
	}
	stopped, err := stopper.door.Stop(
		ctx,
		stopper.target(proc),
		gather.Escalation{Term: termGrace, Kill: killGrace, Poll: pollEvery},
	)
	switch {
	case errors.Is(err, gather.ErrStillRunning):
		return "", fmt.Errorf("pid %d still runs %s after SIGKILL", proc.PID, clipArgv(proc.command()))
	case err != nil:
		return "", fmt.Errorf("stop pid %d: %w", proc.PID, err)
	case stopped == gather.StoppedGone:
		return "already gone", nil
	case stopped == gather.StoppedChanged:
		return "", fmt.Errorf("pid %d identity changed before signal", proc.PID)
	case stopped == gather.StoppedOnTerm:
		return "exited on SIGTERM", nil
	}
	return "killed (SIGKILL after " + termGrace.String() + ")", nil
}

// target is proc as the door's target: the generation the table read, and,
// with an owner, that install's launch held right before each signal. The
// facts verify proves (executable image, config) are fixed for a generation,
// so the door's re-read of start, owner and argv keeps them.
func (stopper processStopper) target(proc process) gather.Target {
	target := gather.Target{
		Identity: gather.Identity{PID: proc.PID, Start: proc.Start, UID: stopper.door.UID, Argv: proc.Argv},
	}
	if stopper.owner != nil {
		owner, procs := *stopper.owner, stopper.door.Procs
		target.Check = func(live gather.Identity) (bool, error) {
			owns, err := owner.Owns(procs, live)
			if gather.IsGone(err) {
				return false, nil // exited mid-read: the door's re-read says gone
			}
			return owns, err
		}
	}
	return target
}

// same reports whether proc's pid still runs proc's command since proc's
// start. A pid the kernel no longer has is not the same; a table that cannot
// be read for a pid that still exists is an error, never "gone".
func (stopper processStopper) same(proc process) (bool, error) {
	procs := stopper.door.Procs
	stat, statErr := procs.Stat(proc.PID)
	argv, argvErr := procs.Cmdline(proc.PID)
	if statErr == nil && argvErr == nil {
		if stat.StartTime != proc.Start {
			return false, nil
		}
		if !slices.Equal(argv, proc.Argv) {
			if stopper.verify != nil {
				if len(argv) == 0 {
					return stopper.verifiedSnapshot(proc)
				}
				return false, fmt.Errorf("pid %d identity changed before signal", proc.PID)
			}
			return false, nil
		}
		if identities, ok := procs.(gather.ProcIdentity); ok {
			identity, err := identities.ProcessIdentity(proc.PID)
			if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
				return false, nil
			}
			if err != nil {
				return false, fmt.Errorf("read pid %d identity before signal: %w", proc.PID, err)
			}
			if identity.Zombie {
				return false, nil
			}
			if identity.EffectiveUID != stopper.door.UID {
				return false, fmt.Errorf("pid %d identity changed before signal", proc.PID)
			}
		}
		if stopper.verify != nil && !stopper.ownershipVerified {
			return stopper.verifiedSnapshot(proc)
		}
		return true, nil
	}
	if stopper.probe != nil && errors.Is(stopper.probe(proc.PID), syscall.ESRCH) {
		return false, nil
	}
	return false, fmt.Errorf("read pid %d from the process table: %w", proc.PID, errors.Join(statErr, argvErr))
}

// verifiedSnapshot rechecks the census after the ownership probe may have waited.
func (stopper processStopper) verifiedSnapshot(proc process) (bool, error) {
	same, err := stopper.verify(proc)
	if err != nil || !same {
		return same, err
	}
	guard := stopper
	guard.ownershipVerified = true
	return guard.same(proc)
}

// findHelpers is the chat-helpers family: every cost sink and stdio MCP server in the process table.
func (finder *discoverer) findHelpers(table processTable) {
	count, kept := 0, 0
	var unowned []error
	for _, proc := range table.Processes {
		kind := finder.helperKind(proc.Argv)
		if kind == "" || finder.claimed[proc.PID] {
			continue
		}
		owned, gone, err := finder.ownedByInstall(proc)
		if err != nil {
			unowned = append(unowned, err)
			continue
		}
		if !owned {
			if !gone {
				kept++
			}
			continue
		}
		finder.claimed[proc.PID] = true
		finder.plan.Units = append(finder.plan.Units, finder.processUnit(FamilyHelpers, kind, proc))
		count++
	}
	finder.found(FamilyHelpers, fmt.Sprintf(
		"%d helper process(es) of %d listed — process table: pfm internal cost-sink, pfm mcp serve --stdio%s",
		count,
		table.Listed,
		keptNote(kept),
	))
	finder.failedOn(FamilyHelpers, errors.Join(append(unowned, table.unreadableErr())...))
}

// ownedByInstall reports whether proc was launched by this install
// (gather.Install.Owns). gone says its pid exited since the table read: a torn
// read of an exiting process is neither this install's nor another's.
func (finder *discoverer) ownedByInstall(proc process) (owned, gone bool, err error) {
	owned, err = finder.deps.Install.Owns(
		finder.deps.Procs,
		gather.Identity{PID: proc.PID, Start: proc.Start, Argv: proc.Argv},
	)
	if gather.IsGone(err) {
		return false, true, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("pid %d install ownership: %w", proc.PID, err)
	}
	return owned, false, nil
}

// keptNote names the processes of another install a family left running.
func keptNote(kept int) string {
	if kept == 0 {
		return ""
	}
	return fmt.Sprintf("; %d of another install kept", kept)
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
	count, kept := 0, 0
	var unowned []error
	for _, proc := range table.Processes {
		if _, ok := finder.pfmArgs(
			proc.Argv,
		); !ok || finder.claimed[proc.PID] ||
			slices.Contains(finder.deps.Spare, proc.PID) {
			continue
		}
		owned, gone, err := finder.ownedByInstall(proc)
		if err != nil {
			unowned = append(unowned, err)
			continue
		}
		if !owned {
			if !gone {
				kept++
			}
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
		"%d other pfm process(es) of %d listed — process table, argv[0] named pfm; %s%s",
		count, table.Listed, last, keptNote(kept)))
	finder.failedOn(FamilyPFM, errors.Join(append(unowned, table.unreadableErr())...))
}
