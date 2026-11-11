package agentopen

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/launchnotice"
	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
	"github.com/rezzminator/professor/pfm/internal/workbench"
)

// ExecCommands is the production command boundary. Every Claude invocation it
// makes is built by action.ClaudeSpawn — the one spawn door — so the hygiene
// strip, the autonomy posture and the system-prompt injection are the fleet's
// single copies rather than a private restatement that drifts.
type ExecCommands struct {
	Home    string
	Machine config.Config
	Stdout  io.Writer
	Stderr  io.Writer
}

// accountFor names the account that owns one config directory. The registry
// query and the resume both address ONE account's directory, and that account
// carries the prompt and autonomy policy the launch must use — so a directory
// that matches no account is an error, never a silent fall back to the primary
// seat's policy under another seat's config dir.
func (commands ExecCommands) accountFor(configDir string) (int, error) {
	for _, account := range commands.Machine.Accounts {
		if configDir != "" && filepath.Clean(account.ConfigDir) == filepath.Clean(configDir) {
			return account.ID, nil
		}
	}
	return 0, fmt.Errorf("config directory %q belongs to no configured Claude account", configDir)
}

func (commands ExecCommands) command(
	ctx context.Context,
	purpose action.Purpose,
	configDir string,
	cache1H bool,
	args ...string,
) (*action.ProcessCommand, error) {
	account, err := commands.accountFor(configDir)
	if err != nil {
		return nil, err
	}
	var cache *bool
	if purpose != action.PurposeQuery {
		cache = &cache1H
	}
	command, err := action.ClaudeSpawn{
		Purpose: purpose,
		Account: account,
		Cache1H: cache,
		Args:    args,
		Home:    commands.Home,
		Machine: commands.Machine,
	}.Command(ctx)
	if err != nil {
		return nil, err
	}
	command.Stdout, command.Stderr = commands.Stdout, commands.Stderr
	return command, nil
}

func (commands ExecCommands) QueryAgents(ctx context.Context, configName string) ([]byte, error) {
	command, err := commands.command(ctx, action.PurposeQuery, configName, false, "agents", "--json")
	if err != nil {
		return nil, fmt.Errorf("query agents: %w", err)
	}
	// Output captures stdout for the registry parser; diagnostics still flow to
	// the caller's stderr through command.Stderr.
	command.Stdout = nil
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("query agents: %w", err)
	}
	return output, nil
}

func (commands ExecCommands) Resume(ctx context.Context, configName, cwd, id string, cache1H bool) error {
	stderr := commands.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	persona, warnings, err := action.LaunchPersona(cwd, pfmengine.Claude, workbench.Resume)
	if err != nil {
		return fmt.Errorf("resume agent session: %w", err)
	}
	launchnotice.Print(stderr, "pfm", warnings)
	account, err := commands.accountFor(configName)
	if err != nil {
		return fmt.Errorf("resume agent session: %w", err)
	}
	// No --name: the resumed session restores its own name from its
	// transcript (claudelaunch.Render gives a resume none).
	spawn := action.ClaudeSpawn{
		Purpose: claudelaunch.PurposeResume,
		Account: account, Cache1H: &cache1H, Resume: id,
		Home: commands.Home, Machine: commands.Machine,
		PromptFile: persona.Prompt, Effort: persona.Effort, Model: persona.Model,
	}
	command, err := spawn.Command(ctx)
	if err != nil {
		return fmt.Errorf("resume agent session: %w", err)
	}
	command.Stdout, command.Stderr = commands.Stdout, commands.Stderr
	command.Dir = cwd
	values, resolveErr := config.ResolvePaths()
	if resolveErr == nil {
		resolveErr = fleetdb.RecordLaunch(ctx, values, fleetdb.Launch{
			SessionID: id,
			Engine:    pfmengine.Claude, Account: account, Cache1H: cache1H,
		}, clock.Real.Now().Unix())
	}
	if resolveErr != nil {
		fmt.Fprintf(stderr, "pfm: record launch %s: %v\n", id, resolveErr)
	}
	// The pane is a terminal whether or not anyone is attached, and the door
	// that attaches has already held: a wait here would stall a detached open
	// and swallow the first injected message as its Enter.
	if warning := spawn.PromptWarning(); warning != "" {
		launchnotice.Print(stderr, "pfm internal agent-open", []string{warning})
	}
	return command.Run()
}

func (commands ExecCommands) View(ctx context.Context, configName, cwd string) error {
	command, err := commands.command(ctx, action.PurposeQuery, configName, false, "agents", "--cwd", cwd)
	if err != nil {
		return fmt.Errorf("open agent view: %w", err)
	}
	return command.Run()
}

type RealProcesses struct{ Root string }

func (processes RealProcesses) Processes(ctx context.Context) ([]Process, error) {
	rows, err := (action.RealProcesses{Root: processes.Root}).Processes(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]Process, 0, len(rows))
	for _, row := range rows {
		result = append(result, Process{PID: row.PID, Argv: row.Argv})
	}
	return result, nil
}

// Alive asks the one pid probe (gather.ProcessAlive); a probe that could not
// tell counts as alive, so nothing resumes over a process that may still run.
func (RealProcesses) Alive(pid int) bool {
	alive, err := gather.ProcessAlive(pid)
	return alive || err != nil && pid > 0
}

func (processes RealProcesses) ParentComm(pid int) string {
	proc := gather.NewProcFS(processes.Root)
	stat, err := proc.Stat(pid)
	if err != nil || stat.ParentPID <= 0 {
		return unknownState
	}
	argv, err := proc.Cmdline(stat.ParentPID)
	if err != nil || len(argv) == 0 {
		return unknownState
	}
	return filepath.Base(argv[0])
}

type RealTmux struct {
	Binary string
	Dir    string
	Stderr io.Writer
}

func (tmux RealTmux) command(ctx context.Context, socket string, args ...string) *pfmtmux.Cmd {
	return pfmtmux.Exec(ctx, tmux.Binary, filepath.Join(tmux.Dir, socket), args...)
}

// SocketForPID finds the Claude socket whose pane runs pid, each server read
// through the one bounded pane read (gather.ProbeServer). A server that did
// not answer is never "not here": unless pid is found elsewhere, it is an error.
func (tmux RealTmux) SocketForPID(ctx context.Context, pid int) (string, error) {
	entries, err := os.ReadDir(tmux.Dir)
	if err != nil {
		return "", err
	}
	var unread []error
	for _, entry := range entries {
		if id, ok := pfmengine.FromSocket(entry.Name()); !ok || id != pfmengine.Claude {
			continue
		}
		state, panes, err := gather.ProbeServer(ctx, tmux.Binary, filepath.Join(tmux.Dir, entry.Name()))
		switch state {
		case gather.ServerDead:
			continue
		case gather.ServerAlive:
			for index := range panes {
				pane := &panes[index]
				if pane.PID == pid {
					return entry.Name(), nil
				}
			}
			continue
		}
		unread = append(unread, fmt.Errorf("list panes on %s: %w", entry.Name(), err))
	}
	return "", errors.Join(unread...)
}

func (tmux RealTmux) Attach(ctx context.Context, socket string) error {
	if err := tmux.command(ctx, socket, "set-option", "-g", "window-size", "latest").Run(); err != nil {
		return fmt.Errorf("set tmux window size: %w", err)
	}
	// An attach lasts as long as its user keeps it: tmux.Exec's default bound
	// would end the session under them.
	return pfmtmux.ExecAttached(ctx, tmux.Binary, filepath.Join(tmux.Dir, socket), "attach").Run()
}
