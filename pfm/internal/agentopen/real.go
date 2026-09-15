package agentopen

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"hostops/pfm/internal/action"
	"hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/gather"
	pfmtmux "hostops/pfm/internal/tmux"
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
		if account.Implicit {
			if configDir == "" || filepath.Clean(configDir) == filepath.Clean(account.ConfigDir) {
				return account.ID, nil
			}
			continue
		}
		if filepath.Clean(account.ConfigDir) == filepath.Clean(configDir) {
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
) (*exec.Cmd, error) {
	account, err := commands.accountFor(configDir)
	if err != nil {
		return nil, err
	}
	command, err := action.ClaudeSpawn{
		Purpose: purpose,
		Account: account,
		Cache1H: cache1H,
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

func (commands ExecCommands) QueryAgents(ctx context.Context, config string) ([]byte, error) {
	command, err := commands.command(ctx, action.PurposeQuery, config, false, "agents", "--json")
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

func (commands ExecCommands) Resume(ctx context.Context, config, cwd, id string, cache1H bool) error {
	command, err := commands.command(ctx, action.PurposeResume, config, cache1H, "--resume", id)
	if err != nil {
		return fmt.Errorf("resume agent session: %w", err)
	}
	command.Dir = cwd
	return command.Run()
}

func (commands ExecCommands) View(ctx context.Context, config, cwd string) error {
	command, err := commands.command(ctx, action.PurposeQuery, config, false, "agents", "--cwd", cwd)
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
func (RealProcesses) Alive(pid int) bool { return pid > 0 && syscall.Kill(pid, 0) == nil }
func (RealProcesses) Terminate(pid int) error {
	err := syscall.Kill(pid, syscall.SIGTERM)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func (RealProcesses) Kill(pid int) error {
	err := syscall.Kill(pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func (processes RealProcesses) ParentComm(pid int) string {
	proc := gather.NewProcFS(processes.Root)
	stat, err := proc.Stat(pid)
	if err != nil || stat.ParentPID <= 0 {
		return "unknown"
	}
	argv, err := proc.Cmdline(stat.ParentPID)
	if err != nil || len(argv) == 0 {
		return "unknown"
	}
	return filepath.Base(argv[0])
}

type RealTmux struct {
	Binary string
	Dir    string
	Stderr io.Writer
}

func (tmux RealTmux) command(ctx context.Context, socket string, args ...string) *exec.Cmd {
	return pfmtmux.Command(ctx, tmux.Binary, filepath.Join(tmux.Dir, socket), args...)
}

func (tmux RealTmux) SocketForPID(ctx context.Context, pid int) (string, error) {
	entries, err := os.ReadDir(tmux.Dir)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if id, ok := pfmengine.FromSocket(entry.Name()); !ok || id != pfmengine.Claude {
			continue
		}
		output, err := tmux.command(ctx, entry.Name(), "list-panes", "-a", "-F", "#{pane_pid}").Output()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(output), "\n") {
			if strings.TrimSpace(line) == fmt.Sprint(pid) {
				return entry.Name(), nil
			}
		}
	}
	return "", nil
}

func (tmux RealTmux) Attach(ctx context.Context, socket string) error {
	if err := tmux.command(ctx, socket, "set-option", "-g", "window-size", "latest").Run(); err != nil {
		return fmt.Errorf("set tmux window size: %w", err)
	}
	return tmux.command(ctx, socket, "attach").Run()
}
