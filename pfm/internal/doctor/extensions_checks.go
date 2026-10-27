package doctor

import (
	"context"
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
)

func printExtensionsDoctor(
	ctx context.Context,
	stdout io.Writer,
	home, claudeDir string,
	machine config.Config,
	tally *doctorTally,
) {
	options := installer.Options{
		Home:               home,
		PrimaryConfigDir:   claudeDir,
		CodexBinary:        machine.Codex.Binary,
		OpenCodeConfigPath: installer.OpenCodeConfigPath(home),
	}
	installer.ConfigureExtensions(&options, machine)
	for _, account := range machine.CodexAccounts {
		options.CodexHomes = append(options.CodexHomes, account.Home)
	}
	checks, err := installer.ExtensionChecks(ctx, installer.ManagedRoot(home), options)
	if err != nil {
		tally.fail()
		fmt.Fprintf(stdout, "doctor: extensions FAIL state unreadable: %v — run pfm install --yes\n", err)
		return
	}
	for _, check := range checks {
		if check.Problem != "" {
			tally.fail()
			fmt.Fprintf(
				stdout,
				"doctor: extension %s/%s %s FAIL %s (%s) — run pfm install --yes\n",
				check.Extension,
				check.Engine,
				check.Aspect,
				check.Problem,
				check.Target,
			)
		} else {
			fmt.Fprintf(
				stdout,
				"doctor: extension %s/%s %s ok (%s)\n",
				check.Extension,
				check.Engine,
				check.Aspect,
				check.Target,
			)
		}
	}
}
