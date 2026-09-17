package action

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"hostops/pfm/internal/deps"
	"hostops/pfm/internal/spawn"
	pfmtmux "hostops/pfm/internal/tmux"
)

// CommandTmux invokes tmux only through the configured jailed socket directory.
type CommandTmux struct {
	Binary  string
	TmuxDir string
}

func (tmux CommandTmux) ListPanes(
	ctx context.Context,
	socket string,
) ([]Pane, error) {
	format := strings.Join([]string{
		"#{pane_id}",
		"#{pane_tty}",
		"#{session_name}",
		"#{window_name}",
		"#{window_index}",
		"#{pane_current_command}",
	}, "\x1f")
	output, err := tmux.command(
		ctx,
		socket,
		"list-panes",
		"-a",
		"-F",
		format,
	).Output()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
	panes := make([]Pane, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		fields := pfmtmux.FormatSplit(line, 6)
		if len(fields) != 6 {
			return nil, fmt.Errorf(
				"tmux socket %q returned %d action fields",
				socket,
				len(fields),
			)
		}
		windowIndex, err := strconv.Atoi(fields[4])
		if err != nil {
			return nil, fmt.Errorf("parse tmux window index %q: %w", fields[2], err)
		}
		panes = append(panes, Pane{
			PaneID:         fields[0],
			TTY:            strings.TrimPrefix(fields[1], "/dev/"),
			SessionName:    fields[2],
			WindowName:     fields[3],
			WindowIndex:    windowIndex,
			CurrentCommand: fields[5],
		})
	}
	return panes, nil
}

func (tmux CommandTmux) SocketAlive(
	ctx context.Context,
	socket string,
) bool {
	return tmux.command(ctx, socket, "list-panes", "-a").Run() == nil
}

func (tmux CommandTmux) KillPane(
	ctx context.Context,
	socket, paneID string,
) error {
	return tmux.command(ctx, socket, "kill-pane", "-t", paneID).Run()
}

func (tmux CommandTmux) KillServer(
	ctx context.Context,
	socket string,
) error {
	return tmux.command(ctx, socket, "kill-server").Run()
}

func (tmux CommandTmux) SetWindowSizeLatest(
	ctx context.Context,
	socket string,
) error {
	return tmux.command(
		ctx,
		socket,
		"set-option",
		"-g",
		"window-size",
		"latest",
	).Run()
}

func (tmux CommandTmux) SelectWindow(
	ctx context.Context,
	socket string,
	windowIndex int,
) error {
	return tmux.command(
		ctx,
		socket,
		"select-window",
		"-t",
		":"+strconv.Itoa(windowIndex),
	).Run()
}

// CreateChatServer creates the plan's server through spawn.CommandTmux.NewSession,
// the one chat-server creator, so a picker-born chat carries the options,
// window name and failure wording of every other door's.
func (tmux CommandTmux) CreateChatServer(
	ctx context.Context,
	server ChatServer,
) error {
	creator := spawn.CommandTmux{Binary: tmux.Binary, TmuxDir: tmux.TmuxDir, Titles: server.Titles}
	return creator.NewSession(ctx, spawn.SessionSpec{
		Socket:  server.Socket,
		Session: server.Socket,
		Window:  server.Window,
		CWD:     server.CWD,
		Run:     server.Run,
	})
}

func (tmux CommandTmux) command(
	ctx context.Context,
	socket string,
	arguments ...string,
) *exec.Cmd {
	return pfmtmux.Command(ctx, tmux.Binary, filepath.Join(tmux.TmuxDir, socket), arguments...)
}

type ExecRunner struct {
	Stdin  *os.File
	Stdout *os.File
	Stderr *os.File
}

func (runner ExecRunner) Run(
	ctx context.Context,
	name string,
	args ...string,
) error {
	command := exec.CommandContext(ctx, deps.Executable(name), args...)
	command.Stdin = runner.Stdin
	command.Stdout = runner.Stdout
	command.Stderr = runner.Stderr
	return command.Run()
}
