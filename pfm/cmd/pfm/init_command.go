package main

import (
	"fmt"
	"io"
	"path/filepath"

	"hostops/pfm/internal/cli"
	pfmconfig "hostops/pfm/internal/config"
	"hostops/pfm/internal/installer"
	"hostops/pfm/internal/professor"
)

func runInit(args []string, stdout, stderr io.Writer, runtimes ...commandRuntime) int {
	flags := cli.NewFlagSet(initCommand, "usage: pfm init [dir] [--force]", stderr)
	force := flags.Bool("force", false, "overwrite colliding scaffold files")
	positional, code, ok := cli.ParseFlagsAnywhere(flags, args)
	if !ok {
		return code
	}
	if len(positional) > 1 {
		flags.Usage()
		return 2
	}
	target := "."
	if len(positional) == 1 {
		target = positional[0]
	}
	target, err := filepath.Abs(target)
	if err != nil {
		fmt.Fprintf(stderr, "pfm init: resolve target: %v\n", err)
		return 1
	}
	runtime, err := pfmconfig.OptionalRuntime(runtimes)
	if err != nil {
		fmt.Fprintf(stderr, "pfm init: config: %v\n", err)
		return 1
	}
	source, err := installer.ReadSourceRepoMarker(runtime.Paths.Home)
	if err != nil {
		fmt.Fprintf(stderr, "pfm init: %v\n", err)
		return 1
	}
	deployed, err := professor.Scaffold(source, target, *force, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "pfm init: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "initialized %s from %s\n", target, source)
	fmt.Fprintf(stdout, "deployed %d project files; baseline: %s\n", deployed, professor.BaselinePath(target))
	fmt.Fprintf(
		stdout,
		"open Claude here and follow %s § Install interview — it fills tokens and deploys per-project agents\n",
		filepath.Join(source, "docs", "SETUP.md"),
	)
	return 0
}
