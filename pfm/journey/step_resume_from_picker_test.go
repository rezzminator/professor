//go:build journey

package journey

import (
	"fmt"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// resumeFromPicker: in the picker the pane shows after the end, the owner finds the ended chat by
// its first word and presses Enter; the resume wizard opens on the chat's last cache, Enter starts
// it, and the chat is back in the pane: one live row on the socket under its old conversation and
// name, on a new engine resumed on it.
func resumeFromPicker(c *harness.Ctx, s State) State {
	term := c.Terminal(s.Term)
	before := s.Chat
	name := resumableName(c, before.Session)
	token, _, _ := strings.Cut(name, " ") // the prompt's first word, unique to this run
	term.WaitFor("↻ " + token)
	term.Type(token)
	term.WaitScreen("the picker narrowed to "+token, stepBound, func(screen string) bool {
		return strings.Contains(screen, "find › "+token)
	})
	if !rowIs(selectedRow(term.Screen()), "↻ ", token) {
		term.Press(harness.Down)
	}
	term.WaitScreen("the ended chat "+token+" selected", stepBound, func(screen string) bool {
		return rowIs(selectedRow(screen), "↻ ", token)
	})
	term.Press(harness.Enter)
	// The resume wizard opens in the row on the chat's last cache, its one account taking no step.
	term.WaitFor("resume · step 1 of 1 · cache")
	term.WaitScreen("the wizard on the chat's last cache, 1h", stepBound, func(screen string) bool {
		return strings.HasPrefix(selectedRow(screen), "↻ ◖ 1h ◗ [ 5m ]")
	})
	term.Press(harness.Enter)
	term.WaitFor("❯", 20*time.Second)
	sock := herdrSocket(s)
	// The chat opens in its project's workspace, and the picker's, emptied, closes: the chat is in the
	// session's only pane, a new one.
	c.Poll("chat "+before.Session+" live again in the session's only pane", 20*time.Second, func() (bool, string) {
		rows, listed := liveChats(c, sock)
		seats := liveSeats(c, sock)
		panes := herdrPanes(c, s)
		seen := fmt.Sprintf("chats %v (listed %v), seats %v, panes %v", rows, listed, seats, panes)
		seat, ok := seats[before.Session]
		if _, inPane := panes[seat[1]]; len(listed) != 1 || len(seats) != 1 || !ok ||
			rows[before.Session][0] != "live-claude" || len(panes) != 1 || !inPane {
			return false, seen
		}
		s.Chat.Seat, s.Pane = seat[0], seat[1]
		return true, seen
	})
	if got := rowName(c, before.Session); got != name {
		c.Failf("the resumed chat is named %q, want %q", got, name)
	}
	s.Chat.Engine = engineOf(c, s)
	if s.Chat.Engine == before.Engine {
		c.Failf("the resumed chat runs on the ended engine %d", before.Engine)
	}
	fleetConsistent(c, s)
	return s
}

// selectedRow is the picker's selected row, from its marker on: the fleet box's "│› " line.
func selectedRow(screen string) string {
	for _, line := range strings.Split(screen, "\n") {
		if _, row, found := strings.Cut(line, "│› "); found {
			return strings.TrimSpace(row)
		}
	}
	return ""
}

// rowIs reports whether a picker row is the chat's: after its mark the row prints the chat's name whole or,
// where the pane leaves the name fewer cells than it has, its start closed by "…" (a selected row gives part
// of its name's cells to the open badge, and the engines listed that day set how many are left).
func rowIs(row, mark, name string) bool {
	rest, marked := strings.CutPrefix(row, mark)
	if !marked {
		return false
	}
	shown, _, _ := strings.Cut(rest, " ")
	if stem, cut := strings.CutSuffix(shown, "…"); cut {
		return len(stem) >= len(name)/2 && strings.HasPrefix(name, stem)
	}
	return shown == name
}

// rowName is the name `pfm ls --tsv` gives session's row, "" when it lists none.
func rowName(c *harness.Ctx, session string) string {
	for _, line := range listRows(c, session) {
		if f := strings.Split(line, "\t"); len(f) > 4 {
			return f[4]
		}
	}
	return ""
}

// resumableName waits for session's row to be resumable and returns its name.
func resumableName(c *harness.Ctx, session string) string {
	name := ""
	c.Poll("chat "+session+" resumable in pfm's list", stepBound, func() (bool, string) {
		rows := listRows(c, session)
		if len(rows) == 1 && strings.HasPrefix(rows[0], "resume-claude\t") {
			name = strings.Split(rows[0], "\t")[4]
			return name != "", rows[0]
		}
		return false, strings.Join(rows, "\n")
	})
	return name
}
