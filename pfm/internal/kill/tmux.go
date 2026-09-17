package kill

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	pfmtmux "hostops/pfm/internal/tmux"
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

func (tmux TmuxKiller) PaneExists(
	ctx context.Context,
	socketPath, paneID string,
) bool {
	output, err := tmux.command(
		ctx,
		socketPath,
		"list-panes",
		"-a",
		"-F",
		"#{pane_id}",
	).Output()
	if err != nil {
		return false
	}
	for _, candidate := range strings.Split(string(output), "\n") {
		if candidate == paneID {
			return true
		}
	}
	return false
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

func (tmux TmuxKiller) KillPane(
	ctx context.Context,
	socketPath, paneID string,
) error {
	return tmux.command(ctx, socketPath, "kill-pane", "-t", paneID).Run()
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
	return tmux.command(ctx, socketPath, "kill-server").Run()
}

func (tmux TmuxKiller) command(
	ctx context.Context,
	socketPath string,
	arguments ...string,
) *exec.Cmd {
	return pfmtmux.Command(ctx, tmux.Binary, socketPath, arguments...)
}
