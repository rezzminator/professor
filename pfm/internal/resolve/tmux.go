package resolve

import (
	"context"
	"fmt"
	"strings"

	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
)

// TmuxResolver invokes tmux with an explicit socket pathname.
type TmuxResolver struct {
	Binary string
}

func (tmux TmuxResolver) ListPanes(
	ctx context.Context,
	socketPath string,
) ([]ResolvedPane, error) {
	format := strings.Join([]string{
		"#{session_name}",
		"#{pane_id}",
		"#{pane_current_command}",
		"#{window_name}",
	}, "\x1f")
	output, err := tmux.command(
		ctx,
		socketPath,
		"list-panes",
		"-a",
		"-F",
		format,
	).Output()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
	panes := make([]ResolvedPane, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		// Either spelling of the control separator: see internal/tmux/format.go.
		fields := pfmtmux.FormatSplit(line, 4)
		if len(fields) != 4 {
			return nil, fmt.Errorf(
				"tmux socket %q returned %d fields in %q",
				socketPath,
				len(fields),
				line,
			)
		}
		panes = append(panes, ResolvedPane{
			SocketPath:     socketPath,
			SessionName:    fields[0],
			PaneID:         fields[1],
			CurrentCommand: fields[2],
			WindowName:     fields[3],
		})
	}
	return panes, nil
}

func (tmux TmuxResolver) CapturePane(
	ctx context.Context,
	socketPath, paneID string,
) (string, error) {
	output, err := tmux.command(
		ctx,
		socketPath,
		"capture-pane",
		"-t",
		paneID,
		"-p",
		"-J",
	).Output()
	return string(output), err
}

func (tmux TmuxResolver) command(
	ctx context.Context,
	socketPath string,
	arguments ...string,
) *pfmtmux.Cmd {
	// Dir stays empty: socketPath here is already the full pathname (kill's
	// shape, per Socket's own doc comment), and filepath.Join("", full)
	// returns full unchanged.
	return pfmtmux.Socket{Binary: tmux.Binary}.Command(ctx, socketPath, arguments...)
}
