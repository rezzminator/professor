package hookentry

import (
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
)

// ClaudeVersion chooses the newest parsed Claude version for the launcher shim.
func ClaudeVersion(args []string, stdout, stderr io.Writer, runtime config.Runtime) int {
	flags := cli.NewFlagSet("internal claude-version", "usage: pfm internal claude-version", stderr)
	if code, ok := cli.ParseFlags(flags, args); !ok {
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
