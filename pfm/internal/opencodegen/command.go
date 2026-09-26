package opencodegen

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/cli"
)

// RunCommand is the operator-facing adapter for the compiler. The cmd/pfm
// package keeps only the top-level dispatch case; flag parsing and rendering
// stay beside the compiler so a second entrypoint cannot drift.
func RunCommand(args []string, resolveRoot func() (string, error), defaultHome string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printOpenCodeUsage(stderr)
		return 2
	}
	if args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printOpenCodeUsage(stdout)
		return 0
	}
	mode, ok := parseMode(args[0])
	if !ok {
		fmt.Fprintf(stderr, "pfm opencode: unknown action %q\n", args[0])
		printOpenCodeUsage(stderr)
		return 2
	}
	flags := cli.NewFlagSet("opencode "+args[0], "usage: pfm opencode build|check|doctor [repo-root] [options]", stderr)
	home := flags.String("home", "", "OpenCode global source/output home")
	positionals, code, parsed := cli.ParseFlagsAnywhere(flags, args[1:])
	if !parsed {
		return code
	}
	if len(positionals) > 1 {
		flags.Usage()
		return 2
	}
	root := ""
	if len(positionals) == 1 {
		var err error
		root, err = filepath.Abs(positionals[0])
		if err != nil {
			fmt.Fprintf(stderr, "pfm opencode %s: resolve repo root: %v\n", args[0], err)
			return 1
		}
	} else {
		var err error
		root, err = resolveRoot()
		if err != nil {
			fmt.Fprintf(stderr, "pfm opencode %s: resolve repo root: %v\n", args[0], err)
			return 1
		}
	}
	homePath := defaultHome
	if *home != "" {
		homePath = *home
	}
	var err error
	if homePath, err = filepath.Abs(homePath); err != nil {
		fmt.Fprintf(stderr, "pfm opencode %s: resolve home: %v\n", args[0], err)
		return 1
	}
	result, err := Compile(Options{Root: root, Home: homePath, Mode: mode})
	if err != nil {
		fmt.Fprintf(stderr, "pfm opencode %s: %v\n", args[0], err)
		return 1
	}
	for _, warning := range result.Warnings {
		fmt.Fprintf(stderr, "pfm opencode: warning: %s\n", warning)
	}
	for _, problem := range result.Problems {
		fmt.Fprintf(stderr, "pfm opencode: %s\n", problem)
	}
	if mode == ModeBuild {
		for _, action := range result.Actions {
			if action.Kind == actionDelete {
				fmt.Fprintf(stdout, "pfm opencode: deleted %s\n", action.Path)
			}
		}
	}
	if !result.OK {
		return 1
	}
	if mode == ModeBuild {
		fmt.Fprintf(
			stdout,
			"OPENCODE BUILD PASS wrote=%d unchanged=%d deleted=%d\n",
			result.Wrote,
			result.Unchanged,
			result.Deleted,
		)
		return 0
	}
	fmt.Fprintf(stdout, "OPENCODE %s PASS\n", strings.ToUpper(args[0]))
	return 0
}

func parseMode(action string) (Mode, bool) {
	switch action {
	case "build":
		return ModeBuild, true
	case "check":
		return ModeCheck, true
	case "doctor":
		return ModeDoctor, true
	default:
		return 0, false
	}
}

func printOpenCodeUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: pfm opencode build|check|doctor [repo-root] [options]")
	fmt.Fprintln(w, "  --home PATH    OpenCode global source/output home")
}
