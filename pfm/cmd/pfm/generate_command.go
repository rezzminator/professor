package main

import (
	"context"
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/installer"
)

var runGenerator = installer.Generate

// runGenerate rebuilds the install outputs compiled from the source clone —
// harness prompts, global command links, engine-sync policy and global
// agents, the Codex config defaults and fleet prompt fence — with no pfm
// rebuild and no full install.
func runGenerate(args []string, stdout, stderr io.Writer, runtimes ...commandRuntime) int {
	flags := cli.NewFlagSet("generate", "usage: pfm generate [--plan]", stderr)
	plan := flags.Bool("plan", false, "preview every output generate would write, writing nothing")
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	mode := installer.ModeApply
	if *plan {
		mode = installer.ModeDryRun
	}
	options := newInstallerOptions(mode, "", false, stdout, stderr, runtimes...)
	if _, err := runGenerator(context.Background(), options); err != nil {
		fmt.Fprintf(stderr, "pfm generate: %v\n", err)
		return 1
	}
	return 0
}
