package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/cli"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/professor"
)

func runInit(args []string, stdout, stderr io.Writer, runtimes ...commandRuntime) int {
	flags := cli.NewFlagSet(initCommand, "usage: pfm init [dir] [--force] | pfm init [dir] --render", stderr)
	force := flags.Bool("force", false, "overwrite colliding scaffold files")
	render := flags.Bool("render", false, "render install-time tokens from .professor/manifest.json tokens")
	positional, code, ok := cli.ParseFlagsAnywhere(flags, args)
	if !ok {
		return code
	}
	if len(positional) > 1 || (*render && *force) {
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
	source, err := paths.ReadSourceRepoMarker(runtime.Paths.Home)
	if err != nil {
		fmt.Fprintf(stderr, "pfm init: %v\n", err)
		return 1
	}
	if *render {
		return runInitRender(source, target, stdout, stderr)
	}
	if code, refused := refuseRescaffold(*force, target, stderr); refused {
		return code
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
		"open Claude here and follow %s § Install interview — it records your answers in .professor/manifest.json, "+
			"runs pfm init --render and deploys the per-project files\n",
		filepath.Join(source, "docs", "SETUP.md"),
	)
	return 0
}

// runInitRender dispatches --render: it never touches refuseRescaffold or
// Scaffold, and it prints the terminal summary line RenderScaffold itself
// does not.
func runInitRender(source, target string, stdout, stderr io.Writer) int {
	rendered, left, err := professor.RenderScaffold(source, target, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "pfm init --render: %v\n", err)
		return 1
	}
	if left > 0 {
		fmt.Fprintf(
			stdout,
			"rendered %d files; %d files still carry install-time tokens (LEFT above)\n",
			rendered,
			left,
		)
	} else {
		fmt.Fprintf(stdout, "rendered %d files; no install-time token left\n", rendered)
	}
	return 0
}

func refuseRescaffold(force bool, target string, stderr io.Writer) (int, bool) {
	switch _, statErr := os.Stat(professor.BaselinePath(target)); {
	case force, os.IsNotExist(statErr):
		return 0, false
	case statErr != nil:
		fmt.Fprintf(stderr, "pfm init: inspect baseline: %v\n", statErr)
		return 1, true
	}
	pinned := ""
	if baseline, err := professor.Load(target); err != nil {
		pinned = fmt.Sprintf("could not be read: %v", err)
	} else {
		pinned = baseline.PinSummary()
	}
	fmt.Fprintf(stderr, "pfm init: %s is already scaffolded (%s exists, %s) — "+
		"a second init would rewrite the baseline; run `pfm doctor --project-updates` to see upstream changes, "+
		"or `pfm init --force` to re-scaffold and re-pin\n", target, professor.BaselinePath(target), pinned)
	return 2, true
}
