// Package stale finds, and sweeps, pfm processes still executing a binary an
// install has since replaced. Replacing ~/.local/bin/pfm renames a new file
// over the path; a process already running keeps the old image, so a days-old
// picker or a chat's stdio MCP server keeps yesterday's behaviour while the
// binary on disk is today's. A process is stale when the file it executes is
// not the file now at the binary's path — compared by device and inode, which
// Linux reads from /proc/<pid>/exe and macOS from lsof, through
// gather.ProcImage. This is the ONE implementation; `make stale` and `make
// sweep-stale` reach it as `pfm internal stale [--sweep]`.
package stale

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// Process is one pfm process running a replaced binary.
type Process struct {
	PID     int
	Command string
	// MCP is true for a chat MCP server (`pfm … mcp …`): sweeping one drops
	// its session's chat tools until that session reconnects.
	MCP                      bool
	compatibleProxyCandidate bool
}

// Scan is one pass over the process table: the stale processes, and every
// live pfm process whose image could not be read — named, because "could not
// look" must never pass as "fresh".
type Scan struct {
	Stale      []Process
	Unreadable []string
}

// Find lists the pfm processes whose executable is not the file at binary.
// A pfm process is one whose argv[0] names a file called pfm, the name
// `pgrep -x pfm` matched; a process that exits mid-scan is skipped, not
// reported.
func Find(table gather.ProcFS, binary string, probe gather.Probe) (Scan, error) {
	images, ok := table.(gather.ProcImage)
	if !ok {
		return Scan{}, errors.New(
			"this process table cannot read which file a process executes, so stale processes cannot be told from fresh ones",
		)
	}
	current, err := gather.FileIDOf(binary)
	if err != nil {
		return Scan{}, fmt.Errorf("identify the installed binary %s: %w", binary, err)
	}
	pids, err := table.PIDs()
	if err != nil {
		return Scan{}, fmt.Errorf("list processes: %w", err)
	}
	var scan Scan
	for _, pid := range pids {
		if pid == os.Getpid() {
			continue
		}
		argv, err := table.Cmdline(pid)
		if err != nil || len(argv) == 0 || filepath.Base(argv[0]) != "pfm" {
			continue
		}
		command := strings.Join(argv, " ")
		image, err := images.Image(pid)
		if err != nil {
			if errors.Is(probe(pid), syscall.ESRCH) {
				continue
			}
			scan.Unreadable = append(scan.Unreadable, fmt.Sprintf("pid=%d  %s: %v", pid, clipCommand(command), err))
			continue
		}
		if image != current {
			scan.Stale = append(
				scan.Stale,
				Process{
					PID: pid, Command: clipCommand(command), MCP: slices.Contains(argv, "mcp"),
					compatibleProxyCandidate: compatibleProxyCommand(argv),
				},
			)
		}
	}
	return scan, nil
}

func compatibleProxyCommand(argv []string) bool {
	if len(argv) == 0 || filepath.Base(argv[0]) != "pfm" {
		return false
	}
	args := argv[1:]
	if len(args) >= 2 && args[0] == "--config" && strings.TrimSpace(args[1]) != "" {
		args = args[2:]
	} else if len(args) >= 1 && strings.HasPrefix(args[0], "--config=") &&
		strings.TrimSpace(strings.TrimPrefix(args[0], "--config=")) != "" {
		args = args[1:]
	}
	return (len(args) == 5 && slices.Equal(args[:4], []string{"mcp", "serve", "--stdio", "--family"}) && args[4] == "chat")
}

func compatibleProxyMarker(home string) string {
	return filepath.Join(home, ".local", "state", "pfm", "mcp-proxy-compatible")
}

func compatibleProxyMarkerTarget(home string) (string, error) {
	marker := compatibleProxyMarker(home)
	target, err := filepath.EvalSymlinks(marker)
	if errors.Is(err, os.ErrNotExist) {
		return filepath.Clean(marker), nil
	}
	if err != nil {
		return "", fmt.Errorf("resolve compatible proxy marker %s: %w", marker, err)
	}
	return filepath.Clean(target), nil
}

// HoldCompatibleProxy marks this process as a stdio proxy that can reconnect
// after the daemon and installed binary are replaced.
func HoldCompatibleProxy(home string) (*os.File, error) {
	marker := compatibleProxyMarker(home)
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		return nil, fmt.Errorf("create compatible proxy marker parent for %s: %w", marker, err)
	}
	handle, err := os.OpenFile(marker, os.O_CREATE|os.O_RDONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open compatible proxy marker %s: %w", marker, err)
	}
	return handle, nil
}

// ClassifyCompatibleProxies separates obsolete replaced-binary processes from
// compatible stdio proxies that hold the canonical marker descriptor. It only
// reads descriptors and liveness; it does not signal or filter the scan.
func ClassifyCompatibleProxies(
	table gather.ProcFS,
	procRoot string,
	home string,
	probe gather.Probe,
	processes []Process,
) ([]Process, []Process, error) {
	marker, err := compatibleProxyMarkerTarget(home)
	if err != nil {
		return nil, nil, err
	}
	var sweepable, kept []Process
	var census gather.ProcFDNames
	var censusErr error
	censusOpened := false
	for _, process := range processes {
		if !process.compatibleProxyCandidate {
			sweepable = append(sweepable, process)
			continue
		}
		links, err := table.FDLinks(process.PID)
		if err != nil {
			if errors.Is(probe(process.PID), syscall.ESRCH) {
				continue
			}
			return nil, nil, fmt.Errorf(
				"inspect descriptors for pid=%d %s against marker %s: %w",
				process.PID, process.Command, marker, err,
			)
		}
		if !censusOpened {
			census, censusErr = descriptorCensus(procRoot)
			censusOpened = true
		}
		if censusErr != nil {
			return nil, nil, fmt.Errorf("inspect descriptor root %s: %w", procRoot, censusErr)
		}
		if census != nil {
			names, readErr := census.FDNames(process.PID)
			if readErr != nil {
				if errors.Is(probe(process.PID), syscall.ESRCH) {
					continue
				}
				return nil, nil, fmt.Errorf(
					"verify descriptor census for pid=%d %s: %w", process.PID, process.Command, readErr,
				)
			}
			read := make(map[int]bool, len(links))
			for _, link := range links {
				read[link.FD] = true
			}
			for _, name := range names {
				fd, parseErr := strconv.Atoi(name)
				if parseErr == nil && fd >= 0 && !read[fd] {
					return nil, nil, fmt.Errorf(
						"inspect descriptors for pid=%d %s: descriptor %d was not readable",
						process.PID, process.Command, fd,
					)
				}
			}
		}
		if slices.ContainsFunc(links, func(link gather.FDLink) bool {
			return filepath.Clean(link.Target) == marker
		}) {
			kept = append(kept, process)
		} else {
			sweepable = append(sweepable, process)
		}
	}
	return sweepable, kept, nil
}

// descriptorCensus is the table that lists every descriptor entry at
// procRoot, or nil where no table can (macOS, a jail root never staged): the
// census then adds nothing to the readable links.
func descriptorCensus(procRoot string) (gather.ProcFDNames, error) {
	table, err := gather.OpenProcFS(procRoot)
	if errors.Is(err, gather.ErrNoProcRoot) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	census, _ := table.(gather.ProcFDNames)
	return census, nil
}

// SweepStaleProcesses TERMs every stale process this install launched, KILLs
// any still there after wait, then scans again and fails naming each survivor
// — readback, not trust. It sweeps only against this install's own binary, so
// another build (a worktree's, a test's) never reads the install's processes
// as stale, and every signal goes through the door: re-identified, still this
// install's, still running a replaced image.
func SweepStaleProcesses(
	door gather.Door,
	install gather.Install,
	binary string,
	procRoot string,
	home string,
	probe gather.Probe,
	stdout io.Writer,
	wait time.Duration,
) error {
	if paths.PhysicalPath(binary) != paths.PhysicalPath(install.Binary) {
		return fmt.Errorf(
			"sweep refused: %s is not this install's pfm %s, and only the installed binary tells this install's stale processes",
			binary,
			install.Binary,
		)
	}
	sweep, err := planSweep(door.Procs, install, binary, procRoot, home, probe)
	if err != nil {
		return err
	}
	for _, process := range sweep.kept {
		fmt.Fprintf(stdout, "sweep: KEEP pid=%d compatible stdio proxy\n", process.PID)
	}
	for _, process := range sweep.foreign {
		fmt.Fprintf(stdout, "sweep: KEEP pid=%d another install's process  %s\n", process.PID, process.Command)
	}
	if len(sweep.targets) == 0 {
		fmt.Fprintln(stdout, "sweep: none — no obsolete pfm process runs a replaced binary")
		return nil
	}
	for _, process := range sweep.processes {
		fmt.Fprintf(stdout, "sweep: TERM pid=%d  %s\n", process.PID, process.Command)
	}
	results := door.StopAll(
		context.Background(),
		sweep.targets,
		gather.Escalation{Term: wait, Kill: wait, Poll: wait / 10},
	)
	mcp := false
	var failures []string
	for index, result := range results {
		process := sweep.processes[index]
		switch {
		case result.Err != nil && errors.Is(result.Err, gather.ErrStillRunning):
			fmt.Fprintf(stdout, "sweep: KILL pid=%d (ignored TERM for %s)\n", process.PID, wait)
			failures = append(failures, fmt.Sprint(process.PID))
		case result.Err != nil:
			failures = append(failures, fmt.Sprintf("%d (%v)", process.PID, result.Err))
		case result.Stopped == gather.StoppedGone:
			fmt.Fprintf(stdout, "sweep: GONE pid=%d exited or was replaced before TERM; not signalled\n", process.PID)
		case result.Stopped == gather.StoppedChanged:
			fmt.Fprintf(stdout, "sweep: SKIP pid=%d changed before TERM; not signalled\n", process.PID)
		case result.Stopped == gather.StoppedKilled:
			fmt.Fprintf(stdout, "sweep: KILL pid=%d (ignored TERM for %s)\n", process.PID, wait)
			mcp = mcp || process.MCP
		case result.Stopped == gather.StoppedOnTerm:
			mcp = mcp || process.MCP
		}
	}
	if len(failures) != 0 {
		return fmt.Errorf("SWEEP-INCOMPLETE: still running a replaced binary: %s", strings.Join(failures, " "))
	}
	left, err := planSweep(door.Procs, install, binary, procRoot, home, probe)
	if err != nil {
		return fmt.Errorf("re-scan after signalling: %w", err)
	}
	if len(left.processes) != 0 {
		pids := make([]string, 0, len(left.processes))
		for _, process := range left.processes {
			pids = append(pids, fmt.Sprint(process.PID))
		}
		return fmt.Errorf("SWEEP-INCOMPLETE: still running a replaced binary: %s", strings.Join(pids, " "))
	}
	if mcp {
		fmt.Fprintln(stdout, "sweep: a session whose chat MCP server was swept reconnects it with /mcp")
	}
	fmt.Fprintln(stdout, "sweep: done — no obsolete pfm process runs a replaced binary")
	return nil
}

// sweepPlan is one discovery pass: the identified targets (processes[i] is
// targets[i]), the compatible proxies kept, and the stale processes another
// install launched, kept too.
type sweepPlan struct {
	targets   []gather.Target
	processes []Process
	kept      []Process
	foreign   []Process
}

// planSweep scans for stale processes, keeps compatible proxies, and captures
// each remaining one's identity. A process this install did not launch is
// kept; one that exits mid-pass is skipped; any other unreadable answer fails
// the pass before a signal is sent.
func planSweep(
	table gather.ProcFS,
	install gather.Install,
	binary string,
	procRoot string,
	home string,
	probe gather.Probe,
) (sweepPlan, error) {
	current, err := gather.FileIDOf(binary)
	if err != nil {
		return sweepPlan{}, fmt.Errorf("identify the installed binary %s: %w", binary, err)
	}
	scan, err := Find(table, binary, probe)
	if err != nil {
		return sweepPlan{}, err
	}
	if len(scan.Unreadable) != 0 {
		return sweepPlan{}, fmt.Errorf(
			"could not read the executable of %d pfm process(es), so the sweep cannot vouch for them: %s",
			len(scan.Unreadable),
			strings.Join(scan.Unreadable, "; "),
		)
	}
	sweepable, kept, err := ClassifyCompatibleProxies(table, procRoot, home, probe, scan.Stale)
	if err != nil {
		return sweepPlan{}, err
	}
	plan := sweepPlan{kept: kept}
	check := staleCheck(table, install, current)
	for _, process := range sweepable {
		identity, err := gather.IdentifyProcess(table, process.PID)
		if gather.IsGone(err) {
			continue
		}
		if err != nil {
			return sweepPlan{}, fmt.Errorf("identify pid=%d %s: %w", process.PID, process.Command, err)
		}
		owned, err := installLaunched(table, install, identity)
		if err != nil {
			return sweepPlan{}, fmt.Errorf("tell whose pid=%d %s is: %w", process.PID, process.Command, err)
		}
		if !owned {
			plan.foreign = append(plan.foreign, process)
			continue
		}
		plan.targets = append(plan.targets, gather.Target{Identity: identity, Check: check})
		plan.processes = append(plan.processes, process)
	}
	return plan, nil
}

// installLaunched is the sweep's ownership match: the process is this
// install's (its mark, or no mark and this install's binary) and executed this
// install's binary path. A stale pfm of this install always did — the install
// renames each new build over that path — while a test or worktree build that
// inherited this install's mark from its shell runs from elsewhere.
func installLaunched(table gather.ProcFS, install gather.Install, live gather.Identity) (bool, error) {
	owned, err := install.Owns(table, live)
	if err != nil || !owned {
		return false, err
	}
	if live.Executable == "" {
		return false, fmt.Errorf("pid %d: the process table cannot read its executable path", live.PID)
	}
	return paths.PhysicalPath(live.Executable) == paths.PhysicalPath(install.Binary), nil
}

// staleCheck is what must still hold right before each signal: the live
// process is this install's and still runs an image other than current.
func staleCheck(table gather.ProcFS, install gather.Install, current gather.FileID) gather.Check {
	return func(live gather.Identity) (bool, error) {
		owned, err := installLaunched(table, install, live)
		if err != nil || !owned {
			return false, err
		}
		images, ok := table.(gather.ProcImage)
		if !ok {
			return false, errors.New("the process table cannot read which file a process executes")
		}
		image, err := images.Image(live.PID)
		if err != nil {
			return false, fmt.Errorf("read pid %d image: %w", live.PID, err)
		}
		return image != current, nil
	}
}

func clipCommand(command string) string {
	if runes := []rune(command); len(runes) > 70 {
		return string(runes[:70])
	}
	return command
}

// Run is `pfm internal stale [--sweep] [--binary PATH]` against the running
// kernel's process table; the installed binary defaults to the one this
// command itself was started from, and a sweep acts for this install only.
func Run(args []string, stdout, stderr io.Writer) int {
	binary, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal stale: locate the installed binary: %v\n", err)
		return 1
	}
	resolved, err := paths.Resolve()
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal stale: %v\n", err)
		return 1
	}
	return runStale(
		args,
		stdout,
		stderr,
		gather.NewDoor(gather.NewProcFS(resolved.ProcRoot)),
		gather.InstallAt(resolved.Home),
		binary,
		resolved.ProcRoot,
		resolved.Home,
		gather.ProbeProcess,
	)
}

func runStale(
	args []string,
	stdout, stderr io.Writer,
	door gather.Door,
	install gather.Install,
	installed string,
	procRoot string,
	home string,
	probe gather.Probe,
) int {
	flags := flag.NewFlagSet("internal stale", flag.ContinueOnError)
	flags.SetOutput(stderr)
	sweep := flags.Bool("sweep", false, "TERM, then KILL, every stale pfm process and prove none is left")
	binaryFlag := flags.String("binary", installed, "the installed pfm every process is compared against")
	flags.Usage = func() { fmt.Fprintln(stderr, "usage: pfm internal stale [--sweep] [--binary PATH]") }
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		if err == nil {
			flags.Usage()
		}
		return 2
	}
	binary := *binaryFlag
	if *sweep {
		if err := SweepStaleProcesses(door, install, binary, procRoot, home, probe, stdout, 3*time.Second); err != nil {
			fmt.Fprintf(stderr, "pfm internal stale: %v\n", err)
			return 1
		}
		return 0
	}
	scan, err := Find(door.Procs, binary, probe)
	if err != nil {
		fmt.Fprintf(stderr, "STALE-UNKNOWN: %v\n", err)
		return 1
	}
	for _, process := range scan.Stale {
		fmt.Fprintf(stdout, "STALE pid=%d  %s\n", process.PID, process.Command)
	}
	for _, unreadable := range scan.Unreadable {
		fmt.Fprintf(stdout, "STALE-UNKNOWN %s\n", unreadable)
	}
	switch {
	case len(scan.Stale) == 0 && len(scan.Unreadable) == 0:
		fmt.Fprintln(stdout, "stale: none — every running pfm process uses the binary now on disk")
	case len(scan.Stale) != 0:
		fmt.Fprintf(stdout, "stale: %d process(es) above are running a REPLACED binary.\n", len(scan.Stale))
		fmt.Fprintln(
			stdout,
			"       They keep behaving like the old build and can write stale state over new fixes. Close them, or run: make sweep-stale",
		)
	}
	if len(scan.Unreadable) != 0 {
		return 1
	}
	return 0
}
