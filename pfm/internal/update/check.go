package update

import (
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/professor"
)

func runUpdateCheckAlias(args []string, stdout, stderr io.Writer, runtimes []config.Runtime) int {
	flags := cli.NewFlagSet("update check", "usage: pfm update check [--root DIR] [--json]", stderr)
	root := flags.String("root", "", "project root used for the template report")
	jsonOutput := flags.Bool(jsonFormat, false, "write the project report as one JSON object")
	positional, code, ok := cli.ParseFlagsAnywhere(flags, args)
	if !ok {
		return code
	}
	if len(positional) != 0 {
		flags.Usage()
		return 2
	}
	runtime, err := config.OptionalRuntime(runtimes)
	if err != nil {
		fmt.Fprintf(stderr, "pfm update check: config: %v\n", err)
		return 3
	}
	fmt.Fprintln(
		stderr,
		"pfm update check: kept for older instructions; the current command is pfm doctor --project-updates",
	)
	return professor.RunProjectUpdates(*root, runtime.Paths.Home, *jsonOutput, stdout, ScanRetiredNames)
}
