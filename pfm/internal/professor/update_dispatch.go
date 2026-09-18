package professor

import (
	"context"
	"fmt"
	"io"

	"hostops/pfm/internal/cli"
	"hostops/pfm/internal/config"
	"hostops/pfm/internal/obs"
)

// RunProjectUpdate runs one project-baseline action. It walks the state door
// (spec § Middleware, `state`) — requested to updated on success, failed
// with the exit code as the error shape otherwise — comp=state, kind=
// professor, never the project root or store path it read.
func RunProjectUpdate(action string, args []string, stdout, stderr io.Writer, runtime config.Runtime) (code int) {
	trail := obs.NewTrail(context.Background(), "professor", "requested")
	defer func() {
		var err error
		if code != 0 {
			err = fmt.Errorf("exit code %d", code)
		} else {
			trail.Reach("updated", action)
		}
		trail.End(err)
	}()
	switch action {
	case "":
		flags := cli.NewFlagSet("update", "usage: pfm update [--root DIR] [--json]", stderr)
		rootFlag := flags.String("root", "", "project root")
		jsonOutput := flags.Bool("json", false, "write one JSON object")
		positional, code, ok := cli.ParseFlagsAnywhere(flags, args)
		if !ok {
			return code
		}
		if len(positional) != 0 {
			flags.Usage()
			return 2
		}
		return runPostUpdate(*rootFlag, *jsonOutput, stdout, runtime)
	case "check":
		flags := cli.NewFlagSet("update check", "usage: pfm update check [--root DIR] [--json]", stderr)
		rootFlag := flags.String("root", "", "project root")
		jsonOutput := flags.Bool("json", false, "write one JSON object")
		positional, code, ok := cli.ParseFlagsAnywhere(flags, args)
		if !ok {
			return code
		}
		if len(positional) != 0 {
			flags.Usage()
			return 2
		}
		root, found, err := resolveProjectRoot(*rootFlag)
		if err != nil {
			writeProjectFailure(stdout, *jsonOutput, err)
			return 1
		}
		if !found {
			writeProjectFailure(stdout, *jsonOutput, errBaselineNotFound)
			return 1
		}
		return renderProjectCheck(root, runtime.Paths.Home, *jsonOutput, stdout)
	case "pin":
		return runProjectPin(args, stdout, stderr, runtime)
	case "drop":
		return runProjectDrop(args, stdout, stderr)
	case "adopt":
		return runProjectAdopt(args, stdout, stderr, runtime)
	case "ignore":
		return runProjectIgnore(args, stdout, stderr, runtime)
	default:
		fmt.Fprintf(stderr, "pfm update: unknown project action %q\n", action)
		return 2
	}
}
