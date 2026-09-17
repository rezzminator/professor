package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"hostops/pfm/internal/agentopen"
	"hostops/pfm/internal/cli"
	"hostops/pfm/internal/fleet"
)

// runInternalAgentOpen is deliberately absent from operator help. It is the
// argv target embedded by the picker for a tmux window, not a user command.
func runInternalAgentOpen(
	args []string,
	stderr io.Writer,
	runtime commandRuntime,
) int {
	flags := cli.NewFlagSet(
		"internal agent-open",
		"usage: pfm internal agent-open --id id --cwd path [--config path]",
		stderr,
	)
	id := flags.String("id", "", "session id")
	cwd := flags.String("cwd", "", "project directory")
	configDir := flags.String(configCommand, "", "owning Claude config directory")
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 || *id == "" || *cwd == "" {
		flags.Usage()
		return 2
	}
	resolved := runtime.Paths
	primary := fleet.PrimaryAccount(resolved, runtime.Config)
	accounts := make([]agentopen.Account, 0, len(runtime.Config.Accounts))
	for _, account := range runtime.Config.Accounts {
		configDir := account.ConfigDir
		if account.Implicit {
			configDir = ""
		}
		accounts = append(accounts, agentopen.Account{ID: account.ID, ConfigDir: configDir})
	}
	opener := agentopen.New(agentopen.Dependencies{
		SIDDir:       resolved.SIDDir,
		Home:         resolved.Home,
		Accounts:     accounts,
		ClaudeBinary: runtime.Config.Claude.Binary,
		Commands: agentopen.ExecCommands{
			// The machine config IS the policy: the spawn door reads the
			// binary, the autonomy posture and the system-prompt choice off
			// the account that owns each config directory.
			Home:    resolved.Home,
			Machine: runtime.Config,
			Stdout:  os.Stdout,
			Stderr:  stderr,
		},
		Processes: agentopen.RealProcesses{Root: resolved.ProcRoot},
		Tmux:      agentopen.RealTmux{Dir: resolved.TmuxDir, Stderr: stderr},
		Stderr:    stderr,
	})
	if err := opener.Open(
		context.Background(),
		agentopen.Request{
			ID:             *id,
			CWD:            *cwd,
			OwningConfig:   *configDir,
			PrimaryAccount: primary,
			Cache1H:        runtime.Config.InitialCache1H(primary),
		},
	); err != nil {
		var outside *agentopen.OutsidePFMError
		if errors.As(err, &outside) {
			fmt.Fprintln(stderr, outside.Error())
			return 1
		}
		fmt.Fprintf(stderr, "pfm internal agent-open: %v\n", err)
		return 1
	}
	return 0
}
