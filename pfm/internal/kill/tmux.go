package kill

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/gather"
	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
)

// TmuxKiller invokes tmux only through an explicit socket pathname.
type TmuxKiller struct {
	Binary string
}

// PaneExists asks the one tmux-server probe (gather.ProbeServer) whether paneID
// is still live. Only tmux's word that the server is dead is "gone" — the
// ordinary shape of the pane's last server closing behind it, the exact moment
// a graceful /exit succeeds. A probe that could not run, a server that did not
// answer within the probe's bound, or any other failure is an error, never
// "false": the kill-exit grace window must not read it as an exit.
func (tmux TmuxKiller) PaneExists(
	ctx context.Context,
	socketPath, paneID string,
) (bool, error) {
	state, panes, err := gather.ProbeServer(ctx, tmux.Binary, socketPath)
	switch state {
	case gather.ServerDead:
		return false, nil
	case gather.ServerAlive:
		return slices.ContainsFunc(panes, func(pane gather.ProbePane) bool { return pane.PaneID == paneID }), nil
	}
	return false, err
}

// KillServerIfSolePane evaluates every session/window's pane IDs inside tmux's
// synchronous command queue. No client-side snapshot can become stale before
// kill-server. Linked windows may repeat a pane ID; that conservatively refuses.
func (tmux TmuxKiller) KillServerIfSolePane(ctx context.Context, socketPath, paneID string) error {
	id, err := strconv.Atoi(strings.TrimPrefix(paneID, "%"))
	if err != nil || id < 0 || !strings.HasPrefix(paneID, "%") {
		return fmt.Errorf("invalid pane ID for exclusive server kill: %q", paneID)
	}
	condition := "#{==:#{S:#{W:#{P:#{pane_id}}}}," + paneID + "}"
	_, err = tmux.command(ctx, socketPath, "if-shell", "-F", "-t", paneID, condition, "kill-server").Output()
	if err != nil {
		return fmt.Errorf("kill exclusive server for pane %s on %s: %w: %s", paneID, socketPath, err, exitStderr(err))
	}
	return nil
}

// exitStderr is what a failed tmux wrote to stderr, for the error text.
func exitStderr(err error) string {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return strings.TrimSpace(string(exitErr.Stderr))
	}
	return ""
}

func (tmux TmuxKiller) KillPane(
	ctx context.Context,
	socketPath, paneID string,
) error {
	_, err := tmux.command(ctx, socketPath, "kill-pane", "-t", paneID).Output()
	if err != nil {
		return fmt.Errorf("kill pane %s on %s: %w: %s", paneID, socketPath, err, exitStderr(err))
	}
	return nil
}

// ClientTTYs lists the terminals attached to this server. A chat's clients ARE
// its viewports — the panes a person is watching it through.
func (tmux TmuxKiller) ClientTTYs(
	ctx context.Context,
	socketPath string,
) ([]string, error) {
	output, err := tmux.command(
		ctx, socketPath, "list-clients", "-F", "#{client_tty}",
	).Output()
	if err != nil {
		return nil, err
	}
	var ttys []string
	for _, line := range strings.Split(string(output), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			ttys = append(ttys, line)
		}
	}
	return ttys, nil
}

// PanesByTTY maps each pane on this server to the terminal it runs in — the
// join that traces a viewport client back to the pane hosting it — read
// through the one bounded pane read (gather.ProbePanes): a hung bunker is
// ErrServerUnresponsive, never "no viewports".
func (tmux TmuxKiller) PanesByTTY(
	ctx context.Context,
	socketPath string,
) (map[string]string, error) {
	listed, err := gather.ProbePanes(ctx, tmux.Binary, socketPath)
	if err != nil {
		return nil, err
	}
	panes := make(map[string]string)
	for index := range listed {
		pane := &listed[index]
		if pane.TTY != "" && pane.PaneID != "" {
			panes[pane.TTY] = pane.PaneID
		}
	}
	return panes, nil
}

func (tmux TmuxKiller) KillServer(
	ctx context.Context,
	socketPath string,
) error {
	_, err := tmux.command(ctx, socketPath, "kill-server").Output()
	if err != nil {
		return fmt.Errorf("kill server on %s: %w: %s", socketPath, err, exitStderr(err))
	}
	return nil
}

// socket is the one tmux-addressing wrapper (internal/tmux.Socket): Dir
// stays empty because every TmuxKiller caller already holds a full
// socketPath, not a bare socket name.
func (tmux TmuxKiller) socket() pfmtmux.Socket {
	return pfmtmux.Socket{Binary: tmux.Binary}
}

func (tmux TmuxKiller) command(
	ctx context.Context,
	socketPath string,
	arguments ...string,
) *pfmtmux.Cmd {
	return tmux.socket().Command(ctx, socketPath, arguments...)
}
