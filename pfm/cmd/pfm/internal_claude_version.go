package main

import (
	"fmt"
	"io"

	"hostops/pfm/internal/installer"
)

// runInternalClaudeVersion is the Go half of D: the launcher shim
// (assets/bin/claude) delegates the versions/ choice here instead of
// picking a candidate by mtime in shell. Three exits: 0 prints the newest
// parsed version's absolute path on stdout; 127 prints nothing — the same
// "no real Claude binary" contract the shim's PATH fallback uses, so a
// caller reading only the exit code never has to special-case which branch
// answered; 1 means InspectClaudeVersions itself could not read the
// versions/ directory (a real read error, not "no versions installed") and
// prints the error to stderr — the shim (F5) lets that one reach the
// terminal instead of discarding it, since exit 127 already prints nothing
// and collapsing 1 into 127 would silently misreport a read error as
// absence.
//
// This runs on EVERY `claude` launch, so it calls installer.InspectClaudeVersions
// alone — enumeration and ordering only, no process table read at all. The
// live-process cross-reference (installer.ProbeLiveClaudeVersions) is a
// separate, deliberately unpaid cost here: it forks a subprocess per pid on
// macOS to identify each running process's image, and this command only
// ever wants the newest build's path, never which builds are live.
func runInternalClaudeVersion(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	flags := newFlagSet("internal claude-version", "usage: pfm internal claude-version", stderr)
	if code, ok := parseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	report, err := installer.InspectClaudeVersions(runtime.Paths.Home, runtime.Config.Claude.Binary)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal claude-version: %v\n", err)
		return 1
	}
	if report.Newest == nil {
		return 127
	}
	fmt.Fprintln(stdout, report.Newest.Path)
	return 0
}
