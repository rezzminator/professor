package main

import (
	"io"

	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/installer"
)

func runUninstall(args []string, stdout, stderr io.Writer, runtimes ...commandRuntime) int {
	flags := cli.NewFlagSet("uninstall", "usage: pfm uninstall [--config-dir DIR] [--leave-vscode-colors]", stderr)
	configDir := flags.String("config-dir", "", "target config directory instead of ~/.claude")
	leaveColors := flags.Bool("leave-vscode-colors", false, "leave VS Code colours; preserve recovery records")
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	options := newInstallerOptions(installer.ModeUninstall, *configDir, false, stdout, stderr, runtimes...)
	options.InstallThemes = true
	options.LeaveVSCodeColors = *leaveColors
	return runInstallerCommand(
		"uninstall",
		options,
		stderr,
	)
}
