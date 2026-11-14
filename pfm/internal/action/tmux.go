package action

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
)

// TmuxExecutor invokes tmux only through the configured jailed socket directory.
type TmuxExecutor struct {
	Binary  string
	TmuxDir string
	// Screen is the pane door LaunchPane reads a screen through; nil is the
	// call engine over Binary (spawn.TmuxSpawner.Pane). A test injects a recorder.
	Screen pfmcall.Screen
}

// ListPanes is the one bounded pane read (gather.ProbePanes): a dead server is
// an error, and a hung one ErrServerUnresponsive, never an empty list.
func (tmux TmuxExecutor) ListPanes(
	ctx context.Context,
	socket string,
) ([]ActionPane, error) {
	listed, err := gather.ProbePanes(ctx, tmux.Binary, filepath.Join(tmux.TmuxDir, socket))
	if err != nil {
		return nil, err
	}
	panes := make([]ActionPane, 0, len(listed))
	for index := range listed {
		pane := &listed[index]
		panes = append(panes, ActionPane{
			PaneID:         pane.PaneID,
			TTY:            strings.TrimPrefix(pane.TTY, "/dev/"),
			SessionName:    pane.SessionName,
			WindowName:     pane.WindowName,
			WindowIndex:    pane.WindowIndex,
			CurrentCommand: pane.CurrentCommand,
		})
	}
	return panes, nil
}

// ServerState asks the one tmux-server probe (gather.ProbeServer) about socket.
func (tmux TmuxExecutor) ServerState(ctx context.Context, socket string) (gather.ServerState, error) {
	state, _, err := gather.ProbeServer(ctx, tmux.Binary, filepath.Join(tmux.TmuxDir, socket))
	return state, err
}

// liveSocketGone is the revive doors' one reading of a live row's server: only
// tmux's word that it is dead resumes the chat fresh; a server that did not
// answer may still hold the chat, so it refuses rather than start a second copy.
func liveSocketGone(ctx context.Context, tmux TmuxClient, socket string) (bool, error) {
	state, err := tmux.ServerState(ctx, socket)
	switch state {
	case gather.ServerDead:
		return true, nil
	case gather.ServerAlive:
		return false, nil
	case gather.ServerUnresponsive:
		return false, fmt.Errorf(
			"live socket %q is unresponsive and may still hold the chat; not resumed: %w",
			socket,
			err,
		)
	default:
		if err == nil {
			err = fmt.Errorf("server state %v with no cause", state)
		}
		return false, fmt.Errorf("probe live socket %q: %w", socket, err)
	}
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

// LaunchPane reads the pane of the session a chat server was created with
// (named after its socket): whether its process exited, its foreground
// command, its terminal, and its screen.
func (tmux TmuxExecutor) LaunchPane(
	ctx context.Context,
	socket string,
) (LaunchPaneState, error) {
	target := socket + ":"
	state, err := tmux.command(
		ctx, socket, "display-message", "-p", "-t", target,
		"#{pane_dead}\x1f#{pane_current_command}\x1f#{pane_tty}",
	).Output()
	if err != nil {
		return LaunchPaneState{}, fmt.Errorf("read the launch pane on socket %q: %w", socket, err)
	}
	fields := pfmtmux.FormatSplit(strings.TrimSuffix(string(state), "\n"), 3)
	if len(fields) != 3 {
		return LaunchPaneState{}, fmt.Errorf("tmux socket %q returned %d launch pane fields", socket, len(fields))
	}
	pane := LaunchPaneState{
		Command: fields[1],
		TTY:     strings.TrimPrefix(fields[2], "/dev/"),
		Dead:    fields[0] == "1",
	}
	door, paneTarget, err := spawn.TmuxSpawner{
		Binary:  tmux.Binary,
		TmuxDir: tmux.TmuxDir,
		Screen:  tmux.Screen,
	}.Pane(
		socket,
		target,
	)
	if err != nil {
		return LaunchPaneState{}, err
	}
	history := 0
	if pane.Dead {
		// tmux's own "Pane is dead" line can push a dying process's last words
		// off a short pane into its history; they are read from there too.
		history = 200
	}
	pane.Screen, err = door.CaptureTarget(ctx, paneTarget, history)
	if err != nil {
		return LaunchPaneState{}, fmt.Errorf("capture the launch pane on socket %q: %w", socket, err)
	}
	return pane, nil
}

// SetRemainOnExit sets or unsets remain-on-exit on the pane of the session a
// chat server was created with.
func (tmux TmuxExecutor) SetRemainOnExit(
	ctx context.Context,
	socket string,
	on bool,
) error {
	target := socket + ":"
	if on {
		return tmux.command(ctx, socket, "set-option", "-p", "-t", target, "remain-on-exit", "on").Run()
	}
	return tmux.command(ctx, socket, "set-option", "-p", "-t", target, "-u", "remain-on-exit").Run()
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
	path, err := deps.Resolve(name)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", name, err)
	}
	process, err := obs.Runner(deps.RealRunner{}).
		Start(ctx, append([]string{path}, args...), deps.StartOptions{
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
