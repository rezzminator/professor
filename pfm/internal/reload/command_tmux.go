package reload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/gather"
	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
)

// CommandTmux is the real Tmux: every pane query and mutation `pfm chat
// reload` needs, routed through pfmtmux.Exec so each invocation completes
// under the observed tmux door. Its keys, typing and reads are call's one
// Screen door (NewCommandTmux), never a copy of its own.
type CommandTmux struct {
	pfmcall.Screen
	// LaunchDir holds the one-shot script an over-budget respawn command
	// launches through (pfmtmux.PrepareLaunch): the fleet's socket directory.
	LaunchDir string
}

// NewCommandTmux is the worker's Tmux, its Screen the call door.
func NewCommandTmux(launchDir string, warnings io.Writer) (CommandTmux, error) {
	screen, err := pfmcall.New(pfmcall.Dependencies{WarningWriter: warnings})
	if err != nil {
		return CommandTmux{}, fmt.Errorf("reload pane door: %w", err)
	}
	return CommandTmux{Screen: screen, LaunchDir: launchDir}, nil
}

func (CommandTmux) command(ctx context.Context, socket string, args ...string) *pfmtmux.Cmd {
	return pfmtmux.Exec(ctx, "", socket, args...)
}

// ListPanes is the one bounded pane read (gather.ProbePanes): a dead server is
// an error, and a hung one ErrServerUnresponsive, never an empty list.
func (tmux CommandTmux) ListPanes(ctx context.Context, socket string) ([]Pane, error) {
	listed, err := gather.ProbePanes(ctx, "", socket)
	if err != nil {
		return nil, fmt.Errorf("list panes: %w", err)
	}
	rows := make([]Pane, 0, len(listed))
	for index := range listed {
		pane := &listed[index]
		rows = append(rows, Pane{
			ID:          pane.PaneID,
			Dead:        pane.Dead,
			CurrentPath: pane.CurrentPath,
			TTY:         strings.TrimPrefix(pane.TTY, "/dev/"),
			PID:         pane.PID,
		})
	}
	return rows, nil
}

func (tmux CommandTmux) SetRemain(ctx context.Context, socket, pane string, on bool) error {
	if on {
		return tmux.command(ctx, socket, "set-option", "-p", "-t", pane, "remain-on-exit", "on").Run()
	}
	return tmux.command(ctx, socket, "set-option", "-p", "-t", pane, "-u", "remain-on-exit").Run()
}

func (tmux CommandTmux) Respawn(ctx context.Context, socket, pane, cwd, command string) error {
	launch, err := pfmtmux.PrepareLaunch(tmux.LaunchDir, command)
	if err != nil {
		return fmt.Errorf("pane %s: %w", pane, err)
	}
	if err := tmux.command(ctx, socket, "respawn-pane", "-k", "-t", pane, "-c", cwd, launch.Command).Run(); err != nil {
		return errors.Join(err, launch.Discard())
	}
	return nil
}

func (tmux CommandTmux) Display(ctx context.Context, socket, pane, message string) error {
	return tmux.command(ctx, socket, "display-message", "-t", pane, message).Run()
}
