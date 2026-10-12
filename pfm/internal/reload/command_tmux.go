package reload

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
)

// CommandTmux is the real Tmux: every pane query and mutation `pfm chat
// reload` needs, routed through pfmtmux.Exec so each invocation completes
// under the observed tmux door.
type CommandTmux struct {
	// LaunchDir holds the one-shot script an over-budget respawn command
	// launches through (pfmtmux.PrepareLaunch): the fleet's socket directory.
	LaunchDir string
}

func (CommandTmux) command(ctx context.Context, socket string, args ...string) *pfmtmux.Cmd {
	return pfmtmux.Exec(ctx, "", socket, args...)
}

func (tmux CommandTmux) ListPanes(ctx context.Context, socket string) ([]Pane, error) {
	format := strings.Join(
		[]string{"#{pane_id}", "#{pane_dead}", "#{pane_current_path}", "#{pane_tty}", "#{pane_pid}"},
		"\x1f",
	)
	output, err := tmux.command(ctx, socket, "list-panes", "-a", "-F", format).Output()
	if err != nil {
		return nil, fmt.Errorf("list panes: %w", err)
	}
	rows := make([]Pane, 0)
	for _, line := range strings.Split(strings.TrimSuffix(string(output), "\n"), "\n") {
		if line == "" {
			continue
		}
		// Either spelling of the control separator: see internal/tmux/format.go.
		fields := pfmtmux.FormatSplit(line, 5)
		if len(fields) != 5 {
			return nil, fmt.Errorf("tmux returned %d pane fields", len(fields))
		}
		pid, err := strconv.Atoi(fields[4])
		if err != nil {
			return nil, fmt.Errorf("parse pane pid %q: %w", fields[4], err)
		}
		rows = append(
			rows,
			Pane{
				ID:          fields[0],
				Dead:        fields[1] == "1",
				CurrentPath: fields[2],
				TTY:         strings.TrimPrefix(fields[3], "/dev/"),
				PID:         pid,
			},
		)
	}
	return rows, nil
}

func (tmux CommandTmux) SetRemain(ctx context.Context, socket, pane string, on bool) error {
	if on {
		return tmux.command(ctx, socket, "set-option", "-p", "-t", pane, "remain-on-exit", "on").Run()
	}
	return tmux.command(ctx, socket, "set-option", "-p", "-t", pane, "-u", "remain-on-exit").Run()
}

func (tmux CommandTmux) PaneInMode(ctx context.Context, socket, pane string) (bool, error) {
	out, err := tmux.command(ctx, socket, "display-message", "-p", "-t", pane, "#{pane_in_mode}").Output()
	return strings.TrimSpace(string(out)) == "1", err
}

func (tmux CommandTmux) CancelMode(ctx context.Context, socket, pane string) error {
	return tmux.command(ctx, socket, "send-keys", "-t", pane, "-X", "cancel").Run()
}

func (tmux CommandTmux) Capture(ctx context.Context, socket, pane string) (string, error) {
	// Reload decisions concern the active TUI only. Including scrollback lets an
	// old composer or selector masquerade as current state.
	out, err := tmux.command(ctx, socket, "capture-pane", "-t", pane, "-p", "-J").Output()
	return string(out), err
}

func (tmux CommandTmux) CaptureStyled(ctx context.Context, socket, pane string) (string, error) {
	out, err := tmux.command(ctx, socket, "capture-pane", "-t", pane, "-p", "-J", "-e").Output()
	return string(out), err
}

func (tmux CommandTmux) SendKey(ctx context.Context, socket, pane, key string) error {
	return tmux.command(ctx, socket, "send-keys", "-t", pane, key).Run()
}

func (tmux CommandTmux) SendLiteral(ctx context.Context, socket, pane, text string) error {
	return tmux.command(ctx, socket, "send-keys", "-t", pane, "-l", "--", text).Run()
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
