package gather

import (
	"fmt"
	"strconv"
	"strings"

	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
)

// paneFormat is the one list-panes format of a pane read — the scan's and
// ProbeServer's: what a pane is, where it runs, and whether its process exited.
//
// pane_current_path is asked for WHOLE, never through tmux's b: basename
// modifier. Pane.CurrentPath is a directory: it becomes a row's CWD, and it
// is the directory the Codex thread resolver matches against the state
// store's absolute threads.cwd. A basename matched nothing there, so every
// live Codex session that holds no rollout descriptor — the normal shape
// since Codex 0.146.1 — silently lost its live row. The project name is
// derived in Go (compose.projectName), where the full path is still there
// to derive it from.
var paneFormat = strings.Join([]string{
	"#{session_name}",
	"#{window_id}",
	"#{window_name}",
	"#{pane_title}",
	"#{pane_tty}",
	"#{pane_pid}",
	"#{pane_id}",
	"#{?session_attached,1,0}",
	"#{pane_current_path}",
	"#{pane_current_command}",
	"#{pane_dead}",
	"#{window_index}",
}, "\x1f")

// partialPanesError is a list-panes answer read in part: the panes returned
// beside it are every line that parsed, and it names the lines that did not.
type partialPanesError struct {
	skipped, total int
	first          error
}

func (partial *partialPanesError) Error() string {
	return fmt.Sprintf("%d of %d pane lines unreadable, the rest kept; the first: %v",
		partial.skipped, partial.total, partial.first)
}

func (partial *partialPanesError) Unwrap() error { return partial.first }

// parseTmuxPanes reads the server's answer to paneFormat, one pane per line.
// A malformed line costs that pane only: the others come back with a
// *partialPanesError naming the loss. An answer with no readable line at all
// is an error and no panes.
func parseTmuxPanes(socket string, output []byte) ([]ProbePane, error) {
	lines := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
	panes := make([]ProbePane, 0, len(lines))
	var partial partialPanesError
	for _, line := range lines {
		if line == "" {
			continue
		}
		partial.total++
		pane, err := parseTmuxPaneLine(socket, line)
		if err != nil {
			partial.skipped++
			if partial.first == nil {
				partial.first = err
			}
			continue
		}
		panes = append(panes, pane)
	}
	switch {
	case partial.skipped == 0:
		return panes, nil
	case len(panes) == 0:
		return nil, partial.first
	}
	return panes, &partial
}

func parseTmuxPaneLine(socket, line string) (ProbePane, error) {
	// The format joins fields with a raw 0x1F, but which spelling comes
	// BACK is tmux-version dependent — older builds render it as the
	// printable escape \037, tmux 3.6 emits the byte. FormatSplit accepts
	// both; assuming one silently reads a whole record as a single field.
	fields := pfmtmux.FormatSplit(line, 12)
	if len(fields) != 12 {
		return ProbePane{}, fmt.Errorf("tmux %s returned %d pane fields in %q", socket, len(fields), line)
	}
	// tmux 3.8 prints no pane_pid once a dead pane's descriptor closes: no live process, PID 0.
	pid := 0
	if fields[5] != "" {
		parsed, err := strconv.Atoi(fields[5])
		if err != nil {
			return ProbePane{}, fmt.Errorf("tmux %s pane pid %q: %w", socket, fields[5], err)
		}
		pid = parsed
	}
	attached, err := strconv.ParseBool(fields[7])
	if err != nil {
		return ProbePane{}, fmt.Errorf("tmux %s attached flag %q: %w", socket, fields[7], err)
	}
	windowIndex, err := strconv.Atoi(fields[11])
	if err != nil {
		return ProbePane{}, fmt.Errorf("tmux %s window index %q: %w", socket, fields[11], err)
	}
	return ProbePane{
		Socket:         socket,
		SessionName:    fields[0],
		WindowID:       fields[1],
		WindowName:     fields[2],
		WindowIndex:    windowIndex,
		PaneTitle:      fields[3],
		CurrentPath:    fields[8],
		CurrentCommand: fields[9],
		TTY:            fields[4],
		PID:            pid,
		PaneID:         fields[6],
		Attached:       attached,
		Dead:           fields[10] == "1",
	}, nil
}
