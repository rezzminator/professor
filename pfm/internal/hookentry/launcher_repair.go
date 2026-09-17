package hookentry

import (
	"fmt"
	"io"

	"hostops/pfm/internal/cli"
	"hostops/pfm/internal/config"
	"hostops/pfm/internal/installer"
)

func LauncherRepair(args []string, stderr io.Writer, runtime config.Runtime) int {
	flags := cli.NewFlagSet("internal launcher-repair", "usage: pfm internal launcher-repair", stderr)
	if code, ok := cli.ParseFlags(flags, args); !ok {
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
