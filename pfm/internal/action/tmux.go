package action

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
)

// TmuxExecutor invokes tmux only through the configured jailed socket directory.
type TmuxExecutor struct {
	Binary  string
	TmuxDir string
}

func (tmux TmuxExecutor) ListPanes(
	ctx context.Context,
	socket string,
) ([]ActionPane, error) {
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
	panes := make([]ActionPane, 0, len(lines))
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
		panes = append(panes, ActionPane{
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

func (tmux TmuxExecutor) SocketAlive(
	ctx context.Context,
	socket string,
) bool {
	return tmux.command(ctx, socket, "list-panes", "-a").Run() == nil
}

func (tmux TmuxExecutor) KillPane(
	ctx context.Context,
	socket, paneID string,
) error {
	return tmux.socket().KillPane(ctx, socket, paneID)
}

func (tmux TmuxExecutor) KillServer(
	ctx context.Context,
	socket string,
) error {
	return tmux.socket().KillServer(ctx, socket)
}

func (tmux TmuxExecutor) SetWindowSizeLatest(
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

func (tmux TmuxExecutor) SelectWindow(
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

// CreateChatServer creates the plan's server through spawn.TmuxSpawner.NewSession,
// the one chat-server creator, so a picker-born chat carries the options,
// window name and failure wording of every other door's.
func (tmux TmuxExecutor) CreateChatServer(
	ctx context.Context,
	server ChatServer,
) error {
	creator := spawn.TmuxSpawner{Binary: tmux.Binary, TmuxDir: tmux.TmuxDir, Titles: server.Titles}
	return creator.NewSession(ctx, spawn.SessionSpec{
		Socket:  server.Socket,
		Session: server.Socket,
		Window:  server.Window,
		CWD:     server.CWD,
		Run:     server.Run,
	})
}

// socket is the one tmux-addressing wrapper (internal/tmux.Socket).
func (tmux TmuxExecutor) socket() pfmtmux.Socket {
	return pfmtmux.Socket{Binary: tmux.Binary, Dir: tmux.TmuxDir}
}

func (tmux TmuxExecutor) command(
	ctx context.Context,
	socket string,
	arguments ...string,
) *pfmtmux.Cmd {
	return tmux.socket().Command(ctx, socket, arguments...)
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
	process, err := obs.Runner(deps.RealRunner{}).
		Start(ctx, append([]string{deps.Executable(name)}, args...), deps.StartOptions{
			Stdin:  runner.Stdin,
			Stdout: runner.Stdout,
			Stderr: runner.Stderr,
		})
	if err != nil {
		return err
	}
	if err := process.Wait(); err != nil {
		return fmt.Errorf("run %s: %w", name, err)
	}
	return nil
}
