package kill

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
)

// TmuxKiller invokes tmux only through an explicit socket pathname.
type TmuxKiller struct {
	Binary string
}

func (tmux TmuxKiller) PanePID(
	ctx context.Context,
	socketPath, paneID string,
) (int, error) {
	output, err := tmux.command(
		ctx,
		socketPath,
		"list-panes",
		"-t",
		paneID,
		"-F",
		"#{pane_pid}",
	).Output()
	if err != nil {
		return 0, err
	}
	value := strings.TrimSpace(string(output))
	pid, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("parse pane pid %q: %w", value, err)
	}
	return pid, nil
}

// PaneExists asks tmux whether paneID is still live. tmux itself failing to
// run (pfmtmux.CouldNotRun) is reported as an error, never folded into
// "false": the caller (the kill-exit grace window) must not read a probe
// that could not run as a pane that has already gone. tmux RUNNING and
// answering "no server on this socket" is the ordinary shape of the pane's
// last server closing behind it — the exact moment a graceful /exit
// succeeds — and stays a plain "gone", not an error.
func (tmux TmuxKiller) PaneExists(
	ctx context.Context,
	socketPath, paneID string,
) (bool, error) {
	output, err := tmux.command(
		ctx,
		socketPath,
		"list-panes",
		"-a",
		"-F",
		"#{pane_id}",
	).Output()
	if err != nil {
		if pfmtmux.CouldNotRun(err) {
			return false, err
		}
		return false, nil
	}
	for _, candidate := range strings.Split(string(output), "\n") {
		if candidate == paneID {
			return true, nil
		}
	}
	return false, nil
}

func (tmux TmuxKiller) SendLine(
	ctx context.Context,
	socketPath, paneID, line string,
) error {
	if output, err := tmux.command(
		ctx,
		socketPath,
		"send-keys",
		"-t",
		paneID,
		"-l",
		"--",
		line,
	).CombinedOutput(); err != nil {
		return fmt.Errorf("send literal line: %w: %s", err, output)
	}
	if output, err := tmux.command(
		ctx,
		socketPath,
		"send-keys",
		"-t",
		paneID,
		"Enter",
	).CombinedOutput(); err != nil {
		return fmt.Errorf("send Enter: %w: %s", err, output)
	}
	return nil
}

// Capture reads paneID's visible screen, joined lines, no history.
func (tmux TmuxKiller) Capture(
	ctx context.Context,
	socketPath, paneID string,
) (string, error) {
	output, err := tmux.command(ctx, socketPath, "capture-pane", "-p", "-J", "-t", paneID).Output()
	if err != nil {
		return "", fmt.Errorf("capture pane %s: %w: %s", paneID, err, exitStderr(err))
	}
	return string(output), nil
}

// CaptureStyled is Capture with the escape sequences kept (-e): the dim
// styling that tells a composer's suggestion from a typed draft.
func (tmux TmuxKiller) CaptureStyled(
	ctx context.Context,
	socketPath, paneID string,
) (string, error) {
	output, err := tmux.command(ctx, socketPath, "capture-pane", "-p", "-J", "-e", "-t", paneID).Output()
	if err != nil {
		return "", fmt.Errorf("capture pane %s styled: %w: %s", paneID, err, exitStderr(err))
	}
	return string(output), nil
}

// exitStderr is what a failed tmux wrote to stderr. A capture reads stdout
// alone: the screen is parsed, and a tmux warning must never join it.
func exitStderr(err error) string {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return strings.TrimSpace(string(exitErr.Stderr))
	}
	return ""
}

// SendKey presses one named tmux key (Enter, Escape) in paneID.
func (tmux TmuxKiller) SendKey(
	ctx context.Context,
	socketPath, paneID, key string,
) error {
	if output, err := tmux.command(ctx, socketPath, "send-keys", "-t", paneID, key).CombinedOutput(); err != nil {
		return fmt.Errorf("send key %s to pane %s: %w: %s", key, paneID, err, output)
	}
	return nil
}

func (tmux TmuxKiller) KillPane(
	ctx context.Context,
	socketPath, paneID string,
) error {
	return tmux.socket().KillPane(ctx, socketPath, paneID)
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
// join that traces a viewport client back to the pane hosting it.
//
// Space-delimited, never a tab: tmux hands a control character in a format
// string back as "_" unless the caller's environment carries a UTF-8 locale or
// merely defines $TMUX, and a row that arrives "_"-joined is silently
// unsplittable. A tty is /dev/pts/N and a pane is %N, so a space cannot be
// ambiguous.
func (tmux TmuxKiller) PanesByTTY(
	ctx context.Context,
	socketPath string,
) (map[string]string, error) {
	output, err := tmux.command(
		ctx, socketPath, "list-panes", "-a", "-F", "#{pane_tty} #{pane_id}",
	).Output()
	if err != nil {
		return nil, err
	}
	panes := make(map[string]string)
	for _, line := range strings.Split(string(output), "\n") {
		tty, paneID, found := strings.Cut(strings.TrimSpace(line), " ")
		if found && tty != "" && paneID != "" {
			panes[tty] = paneID
		}
	}
	return panes, nil
}

func (tmux TmuxKiller) KillServer(
	ctx context.Context,
	socketPath string,
) error {
	return tmux.socket().KillServer(ctx, socketPath)
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
