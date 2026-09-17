package main

import (
	"fmt"
	"io"

	"hostops/pfm/internal/installer"
)

func runInternalLauncherRepair(args []string, stderr io.Writer, runtime commandRuntime) int {
	flags := newFlagSet("internal launcher-repair", "usage: pfm internal launcher-repair", stderr)
	if code, ok := parseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	if _, err := installer.RepairClaudeLauncher(runtime.Paths.Home); err != nil {
		fmt.Fprintf(stderr, "pfm internal launcher-repair: %v\n", err)
		return 1
	}
	return 0
}
