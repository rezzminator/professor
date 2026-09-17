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
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"hostops/pfm/internal/gather"
	"hostops/pfm/internal/paths"
)

// Process is one pfm process running a replaced binary.
type Process struct {
	PID     int
	Command string
	// MCP is true for a chat MCP server (`pfm … mcp …`): sweeping one drops
	// its session's chat tools until that session reconnects.
	MCP bool
}

// Scan is one pass over the process table: the stale processes, and every
// live pfm process whose image could not be read — named, because "could not
// look" must never pass as "fresh".
type Scan struct {
	Stale      []Process
	Unreadable []string
}

// Signaler delivers a signal to pid; signal 0 only asks whether pid exists.
// syscall.Kill is the real one; a test's fake plays the kernel.
type Signaler func(pid int, signal syscall.Signal) error

// Find lists the pfm processes whose executable is not the file at binary.
// A pfm process is one whose argv[0] names a file called pfm, the name
// `pgrep -x pfm` matched; a process that exits mid-scan is skipped, not
// reported.
func Find(table gather.ProcFS, binary string, signal Signaler) (Scan, error) {
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
			if errors.Is(signal(pid, 0), syscall.ESRCH) {
				continue
			}
			scan.Unreadable = append(scan.Unreadable, fmt.Sprintf("pid=%d  %s: %v", pid, clipCommand(command), err))
			continue
		}
		if image != current {
			scan.Stale = append(
				scan.Stale,
				Process{PID: pid, Command: clipCommand(command), MCP: slices.Contains(argv, "mcp")},
			)
		}
	}
	return scan, nil
}

// Sweep TERMs every stale process, KILLs any still there after wait, then
// scans again and fails naming each survivor — readback, not trust. It only
// ever signals images that are not the installed binary, so the binary just
// installed, and the daemon restarted on it, are never touched.
func SweepStaleProcesses(
	table gather.ProcFS,
	binary string,
	signal Signaler,
	stdout io.Writer,
	wait time.Duration,
) error {
	scan, err := Find(table, binary, signal)
	if err != nil {
		return err
	}
	if len(scan.Unreadable) != 0 {
		return fmt.Errorf(
			"could not read the executable of %d pfm process(es), so the sweep cannot vouch for them: %s",
			len(scan.Unreadable),
			strings.Join(scan.Unreadable, "; "),
		)
	}
	if len(scan.Stale) == 0 {
		fmt.Fprintln(stdout, "sweep: none — every running pfm process uses the binary now on disk")
		return nil
	}
	mcp := false
	for _, process := range scan.Stale {
		mcp = mcp || process.MCP
		fmt.Fprintf(stdout, "sweep: TERM pid=%d  %s\n", process.PID, process.Command)
		deliver(signal, process.PID, syscall.SIGTERM)
	}
	survivors, err := awaitExit(table, binary, signal, wait)
	if err != nil {
		return err
	}
	for _, process := range survivors {
		fmt.Fprintf(stdout, "sweep: KILL pid=%d (ignored TERM for %s)\n", process.PID, wait)
		deliver(signal, process.PID, syscall.SIGKILL)
	}
	if len(survivors) != 0 {
		if survivors, err = awaitExit(table, binary, signal, wait); err != nil {
			return err
		}
	}
	if len(survivors) != 0 {
		pids := make([]string, 0, len(survivors))
		for _, process := range survivors {
			pids = append(pids, fmt.Sprint(process.PID))
		}
		return fmt.Errorf("SWEEP-INCOMPLETE: still running a replaced binary: %s", strings.Join(pids, " "))
	}
	if mcp {
		fmt.Fprintln(stdout, "sweep: a session whose chat MCP server was swept reconnects it with /mcp")
	}
	fmt.Fprintln(stdout, "sweep: done — no pfm process runs a replaced binary")
	return nil
}

// awaitExit re-scans until no stale process is left or wait runs out, and
// returns what is left. A re-scan that fails is an error, never an empty
// "all gone".
func awaitExit(table gather.ProcFS, binary string, signal Signaler, wait time.Duration) ([]Process, error) {
	deadline := time.Now().Add(wait)
	for {
		scan, err := Find(table, binary, signal)
		if err != nil {
			return nil, fmt.Errorf("re-scan after signalling: %w", err)
		}
		if len(scan.Stale) == 0 || time.Now().After(deadline) {
			return scan.Stale, nil
		}
		time.Sleep(wait / 10)
	}
}

// deliver signals one real process. A pid of 0 or below is refused outright:
// kill(2) reads 0 as the caller's process group and -1 as every process the
// user owns.
func deliver(signal Signaler, pid int, which syscall.Signal) {
	if pid > 0 {
		_ = signal(pid, which)
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
// command itself was started from.
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
	return run(args, stdout, stderr, gather.NewProcFS(resolved.ProcRoot), binary, syscall.Kill)
}

func run(args []string, stdout, stderr io.Writer, table gather.ProcFS, installed string, signal Signaler) int {
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
		if err := SweepStaleProcesses(table, binary, signal, stdout, 3*time.Second); err != nil {
			fmt.Fprintf(stderr, "pfm internal stale: %v\n", err)
			return 1
		}
		return 0
	}
	scan, err := Find(table, binary, signal)
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
