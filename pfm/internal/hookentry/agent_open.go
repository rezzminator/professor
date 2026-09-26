package hookentry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/rezzminator/professor/pfm/internal/agentopen"
	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleet"
)

// AgentOpen is deliberately absent from operator help. It is the argv target
// embedded by the picker for a tmux window, not a user command.
func AgentOpen(args []string, stderr io.Writer, runtime config.Runtime) int {
	flags := cli.NewFlagSet(
		"internal agent-open",
		"usage: pfm internal agent-open --id id --cwd path [--config path]",
		stderr,
	)
	id := flags.String("id", "", "session id")
	cwd := flags.String("cwd", "", "project directory")
	configDir := flags.String("config", "", "owning Claude config directory")
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 || *id == "" || *cwd == "" {
		flags.Usage()
		return 2
	}
	resolved := runtime.Paths
	primary, primaryErr := fleet.PrimaryAccount(resolved, runtime.Config)
	if primaryErr != nil {
		fmt.Fprintf(stderr, "pfm internal agent-open: read primary account: %v\n", primaryErr)
		return 1
	}
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
			Home: resolved.Home, Machine: runtime.Config, Stdout: os.Stdout, Stderr: stderr,
		},
		Processes: agentopen.RealProcesses{Root: resolved.ProcRoot},
		Tmux:      agentopen.RealTmux{Dir: resolved.TmuxDir, Stderr: stderr},
		Stderr:    stderr,
	})
	if err := opener.Open(context.Background(), agentopen.Request{
		ID: *id, CWD: *cwd, OwningConfig: *configDir, PrimaryAccount: primary,
		Cache1H: runtime.Config.InitialCache1H(primary),
	}); err != nil {
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
