package mockengine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"
)

// agentPanel is the background agents a pane runs behind its idle composer: the row each draws
// under the composer, and the agents whose gate is gone, waiting for the turn that delivers
// their result.
type agentPanel struct {
	rows     []agentRow
	finished []agentRow
	// focused is set while the panel holds the keyboard (agentPanelKey).
	focused bool
}

// agentRow is one background agent: its row as the engine draws it, and, when its step named
// until_gone, the file it runs until and the busy time of the turn that reports its end.
type agentRow struct {
	line, name, gate string
	busyMS           int
}

// agentTick is how often an idle pane looks at its agents' gates.
const agentTick = 50 * time.Millisecond

func (panel *agentPanel) render() string {
	var out strings.Builder
	const indent = "  "
	if panel.focused {
		// Claude Code 2.1.280's focused panel: its footer hint, then the cursor on the bare main row
		// (internal/call/agentpanel.go reads both).
		out.WriteString(agentPanelHint + "\r\n❯ ⏺ main\r\n")
	}
	for _, row := range panel.rows {
		line := row.line
		if panel.focused {
			// The cursor is the panel's, on main: an agent row draws none.
			line = indent + strings.TrimPrefix(line, "❯ ")
		}
		out.WriteString(line + "\r\n")
	}
	return out.String()
}

// agentPanelHint is the footer Claude Code's agents panel draws while it holds the keyboard.
const agentPanelHint = "  ↑/↓ to select · Enter to view"

// agentPanelKey plays the agents panel's hold on the keyboard and reports whether it took the key: the
// left arrow on an empty Claude composer over running agents focuses the panel; while focused,
// Escape hands the keyboard back to the composer and every other key is the panel's.
func (loop *paneLoop) agentPanelKey(event keyEvent) bool {
	screen := loop.screen
	panel := &screen.agents
	if panel.focused {
		if event.kind == keyEscape {
			panel.focused = false
		}
		return true
	}
	if event.kind == keyLeft && screen.rule != "" && len(panel.rows) > 0 && len(screen.draft.draft) == 0 {
		panel.focused = true
		return true
	}
	return false
}

// agentWaitLine is the row Claude Code draws above its input box while background agents run
// behind an idle composer (internal/call/guards.go reads it as not busy; Herdr reads it as
// working). Claude alone draws the box, so a pane without its rule draws no such row.
func (screen *pane) agentWaitLine() string {
	count := len(screen.agents.rows)
	if screen.rule == "" || count == 0 || screen.footer != "" {
		return ""
	}
	plural := "s"
	if count == 1 {
		plural = ""
	}
	return fmt.Sprintf("\r\n✻ Waiting for %d background agent%s to finish", count, plural)
}

// agentsDue ticks while an agent runs until a gate or a finished one waits for its turn; nil,
// which never fires, otherwise.
func (loop *paneLoop) agentsDue() <-chan time.Time {
	panel := &loop.screen.agents
	if len(panel.finished) > 0 {
		return time.After(agentTick)
	}
	for _, row := range panel.rows {
		if row.gate != "" {
			return time.After(agentTick)
		}
	}
	return nil
}

// agentsEnded ends every agent whose gate is gone: its row goes at once, open question or not,
// and once no question is open each ended agent's result arrives as Claude delivers it, a turn of
// its own (the busy footer for its busy_ms, then `⏺ Agent "{name}" finished`, then Stop). A gate
// that cannot be statted keeps its agent running and says why.
func (loop *paneLoop) agentsEnded() (int, bool) {
	screen := loop.screen
	panel := &screen.agents
	kept := panel.rows[:0]
	for _, row := range panel.rows {
		if row.gate == "" {
			kept = append(kept, row)
			continue
		}
		_, err := os.Stat(row.gate)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			panel.finished = append(panel.finished, row)
		case err != nil:
			loop.warn("stat background agent %s gate %s: %v", row.name, row.gate, err)
			kept = append(kept, row)
		default:
			kept = append(kept, row)
		}
	}
	if len(kept) != len(panel.rows) {
		panel.rows = kept
		panel.focused = panel.focused && len(kept) > 0
		loop.paint()
	}
	for screen.menu == nil && len(panel.finished) > 0 {
		row := panel.finished[0]
		panel.finished = panel.finished[1:]
		if notifier, ok := loop.engine.(interface{ taskNotification(string) error }); ok {
			if err := notifier.taskNotification(row.name); err != nil {
				loop.warn("task notification for %s: %v", row.name, err)
			}
		}
		report := Step{Type: StepTurn, Reply: `Agent "` + row.name + `" finished`, BusyMS: row.busyMS}
		if code, exited := loop.turn(loop.proc.script.only([]Step{report})); exited {
			return code, true
		}
	}
	return 0, false
}
