package main

import (
	"io"

	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/installer"
)

func runUninstall(args []string, stdout, stderr io.Writer, runtimes ...commandRuntime) int {
	flags := cli.NewFlagSet(
		"uninstall",
		"usage: pfm uninstall [--config-dir DIR]",
		stderr,
	)
	configDir := flags.String("config-dir", "", "target config directory instead of ~/.claude")
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	options := newInstallerOptions(installer.ModeUninstall, *configDir, false, stdout, stderr, runtimes...)
	options.InstallThemes = true
	return runInstallerCommand(
		"uninstall",
		options,
		stderr,
	)
}
