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
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
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

// ConvergeMCPDaemon hands off only this install's unmanaged loopback daemon.
// It uses kernel executable identity and the process's effective config, never basename alone.
func ConvergeMCPDaemon(ctx context.Context, dependencies Deps, enabled bool) (string, error) {
	finder := &discoverer{deps: dependencies}
	table, err := finder.readTable()
	if err != nil {
		return "", fmt.Errorf("process check: %w", err)
	}
	if err := table.unreadableErr(); err != nil {
		return "", fmt.Errorf("process check: %w", err)
	}
	var owned []process
	for _, proc := range table.Processes {
		match, err := finder.ownedMCPDaemon(ctx, proc)
		if err != nil {
			return "", err
		}
		if match == daemonOwned {
			owned = append(owned, proc)
		}
	}
	if len(owned) == 0 {
		return "process enumeration confirms no daemon owned by this install", nil
	}
	stopper := processStopper{
		procs:  dependencies.Procs,
		signal: dependencies.Signal,
		clock:  dependencies.Clock,
		uid:    dependencies.EffectiveUID,
		verify: func(proc process) (bool, error) {
			state, err := finder.ownedMCPDaemon(ctx, proc)
			if err != nil {
				return false, err
			}
			switch state {
			case daemonGone:
				return false, nil
			case daemonForeign:
				return false, fmt.Errorf("pid %d daemon identity changed before signal", proc.PID)
			default:
				return true, nil
			}
		},
	}
	for _, proc := range owned {
		if proc.Start == 0 {
			return "", fmt.Errorf("pid %d daemon start identity unavailable", proc.PID)
		}
	}
	for _, proc := range owned {
		if _, err := stopper.stop(ctx, proc); err != nil {
			return "", fmt.Errorf("stop owned MCP daemon: %w", err)
		}
	}
	if !enabled {
		return fmt.Sprintf("stopped %d owned unmanaged MCP daemon(s)", len(owned)), nil
	}
	return finder.startDaemon(ctx)
}

type daemonMissingRead struct {
	door string
	err  error
}

func (err *daemonMissingRead) Error() string { return err.door + ": " + err.err.Error() }
func (err *daemonMissingRead) Unwrap() error { return err.err }

func (finder *discoverer) ownedMCPDaemon(ctx context.Context, proc process) (daemonOwnership, error) {
	deadline := finder.deps.Clock.Now().Add(termGrace)
	waited := false
	for {
		if err := ctx.Err(); err != nil {
			return daemonForeign, fmt.Errorf("prove daemon pid %d exit: %w", proc.PID, err)
		}
		state, err := finder.savedDaemonIdentity(proc)
		if state == daemonOwned && err == nil {
			state, err = finder.inspectOwnedMCPDaemon(proc)
		}
		if err == nil {
			if waited && state == daemonOwned {
				state, err = finder.savedDaemonIdentity(proc)
				if state == daemonOwned && err == nil {
					state, err = finder.inspectOwnedMCPDaemon(proc)
				}
				if state == daemonOwned && err == nil {
					state, err = finder.savedDaemonIdentity(proc)
				}
				if state == daemonOwned && err == nil {
					state, err = finder.daemonIdentityState(proc)
				}
			}
			if err == nil {
				return state, nil
			}
		}
		var missing *daemonMissingRead
		if !errors.As(err, &missing) || (!errors.Is(err, fs.ErrNotExist) && !errors.Is(err, syscall.ESRCH)) {
			return daemonForeign, fmt.Errorf("pid %d daemon ownership: %w", proc.PID, err)
		}
		state, proofErr := finder.daemonIdentityState(proc)
		if proofErr != nil {
			return daemonForeign, fmt.Errorf("pid %d daemon %s: %w", proc.PID, missing.door, errors.Join(err, proofErr))
		}
		if state != daemonOwned {
			return state, nil
		}
		if !finder.deps.Clock.Now().Before(deadline) {
			return daemonForeign, fmt.Errorf(
				"deadline proving daemon pid %d %s after %s: %w",
				proc.PID,
				missing.door,
				termGrace,
				err,
			)
		}
		if err := finder.deps.Clock.Sleep(ctx, pollEvery); err != nil {
			return daemonForeign, fmt.Errorf("wait for daemon pid %d exit proof: %w", proc.PID, err)
		}
		waited = true
	}
}

// savedDaemonIdentity never replaces the census's start time or argv with a later process.
func (finder *discoverer) savedDaemonIdentity(proc process) (daemonOwnership, error) {
	stat, statErr := finder.deps.Procs.Stat(proc.PID)
	argv, argvErr := finder.deps.Procs.Cmdline(proc.PID)
	if statErr != nil || argvErr != nil {
		state, err := finder.daemonIdentityState(proc)
		if err != nil {
			return daemonForeign, errors.Join(statErr, argvErr, err)
		}
		if state == daemonGone {
			return state, nil
		}
		readErr := errors.Join(statErr, argvErr)
		return daemonForeign, fmt.Errorf("read saved pid %d identity: %w", proc.PID, readErr)
	}
	if stat.StartTime != proc.Start {
		return daemonGone, nil
	}
	if len(argv) == 0 {
		return daemonForeign, &daemonMissingRead{door: "command line", err: fs.ErrNotExist}
	}
	if !slices.Equal(argv, proc.Argv) {
		return daemonForeign, nil
	}
	return daemonOwned, nil
}

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

func (finder *discoverer) inspectOwnedMCPDaemon(proc process) (daemonOwnership, error) {
	if !finder.mcpDaemon(proc.Argv) {
		return daemonForeign, nil
	}
	state, err := finder.daemonIdentityState(proc)
	if err != nil || state != daemonOwned {
		return state, err
	}
	executables, ok := finder.deps.Procs.(gather.ProcExecutable)
	if !ok {
		return daemonForeign, fmt.Errorf("pid %d daemon executable reader unavailable", proc.PID)
	}
	executable, err := executables.Executable(proc.PID)
	if err != nil {
		return daemonForeign, &daemonMissingRead{door: "executable", err: err}
	}
	if !filepath.IsAbs(finder.deps.Executable) ||
		paths.PhysicalPath(executable) != paths.PhysicalPath(finder.deps.Executable) {
		return daemonForeign, nil
	}
	args := proc.Argv[1:]
	configPath := ""
	switch {
	case len(args) >= 2 && args[0] == "--config":
		configPath, args = args[1], args[2:]
	case len(args) >= 1 && strings.HasPrefix(args[0], "--config="):
		configPath, args = strings.TrimPrefix(args[0], "--config="), args[1:]
	}
	if !slices.Equal(args, []string{mcpCommand, mcpServeCommand}) {
		return daemonForeign, nil
	}
	if configPath == "" {
		environment, err := finder.deps.Procs.Environ(proc.PID)
		if err != nil {
			return daemonForeign, &daemonMissingRead{door: "environment", err: err}
		}
		configPath, err = config.ResolvePathFrom(&paths.MapEnv{Values: environment}, environment["HOME"])
		if err != nil {
			return daemonForeign, fmt.Errorf("pid %d daemon config: %w", proc.PID, err)
		}
	}
	if !filepath.IsAbs(configPath) || !filepath.IsAbs(finder.deps.ConfigPath) {
		return daemonForeign, fmt.Errorf("pid %d daemon config identity requires absolute paths", proc.PID)
	}
	if paths.PhysicalPath(configPath) != paths.PhysicalPath(finder.deps.ConfigPath) {
		return daemonForeign, nil
	}
	return daemonOwned, nil
}

// processUnit is the stop of one process: SIGTERM, then SIGKILL, each
// confirmed against the table.
func (finder *discoverer) processUnit(family, kind string, proc process) Unit {
	stopper := processStopper{
		procs:  finder.deps.Procs,
		signal: finder.deps.Signal,
		clock:  finder.deps.Clock,
		uid:    finder.deps.EffectiveUID,
	}
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
	procs             gather.ProcFS
	signal            Signaler
	clock             clock.Clock
	uid               uint32
	verify            func(process) (bool, error)
	ownershipVerified bool
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
		if identities, ok := stopper.procs.(gather.ProcIdentity); ok {
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
			if identity.EffectiveUID != stopper.uid {
				return false, fmt.Errorf("pid %d identity changed before signal", proc.PID)
			}
		}
		if stopper.verify != nil && !stopper.ownershipVerified {
			return stopper.verifiedSnapshot(proc)
		}
		return true, nil
	}
	if err := stopper.signal(proc.PID, 0); errors.Is(err, syscall.ESRCH) {
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

// findHelpers is the chat-helpers family: every cost sink and stdio MCP server in the process table.
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
		"%d helper process(es) of %d listed — process table: pfm internal cost-sink, pfm mcp serve --stdio",
		count,
		table.Listed,
	))
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
