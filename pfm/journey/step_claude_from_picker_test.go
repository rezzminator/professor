//go:build journey

package journey

import (
	"fmt"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// claudeFromPicker: in the first pane of the branch's Herdr session the owner picks a new Claude chat
// and walks the wizard with Enter; the fake Claude draws in the pane, pfm lists exactly one live Claude
// chat on that session's socket, its seat is live on that pane and its engine runs under the pane's holder.
func claudeFromPicker(c *harness.Ctx, s State) State {
	term := c.Terminal(s.Term)
	term.Press(harness.Enter)
	term.WaitFor("choose the cache")
	term.Press(harness.Enter)
	term.WaitFor("❯", 20*time.Second)
	c.Poll("one live Claude chat with a live seat on "+s.Pane, 20*time.Second, func() (bool, string) {
		rows, _ := liveChats(c, herdrSocket(s))
		seats := liveSeats(c, herdrSocket(s))
		seen := fmt.Sprintf("chats %v, seats %v", rows, seats)
		if len(rows) != 1 || len(seats) != 1 {
			return false, seen
		}
		for session, row := range rows {
			seat, ok := seats[session]
			if !ok || row != [2]string{"live-claude", herdrSocket(s)} || seat[1] != s.Pane {
				return false, seen
			}
			s.Chat = Chat{Session: session, Seat: seat[0]}
		}
		return true, seen
	})
	s.Chat.Engine = engineOf(c, s)
	fleetConsistent(c, s)
	return s
}

// engineOf waits for the one engine process of s.Chat and requires it under the pane's holder.
func engineOf(c *harness.Ctx, s State) int {
	pid := 0
	what := "one engine of session " + s.Chat.Session + " under the pane's holder " + s.Holder
	c.Poll(what, stepBound, func() (bool, string) {
		table := processes(c)
		var found []int
		for p, session := range engines(table) {
			if session == s.Chat.Session {
				found = append(found, p)
			}
		}
		if len(found) != 1 {
			return false, fmt.Sprintf("engines %v", found)
		}
		if held, seen := heldByPane(c, s, table, found[0]); !held {
			return false, fmt.Sprintf("engine %d not under the pane's holder: %s", found[0], seen)
		}
		pid = found[0]
		return true, ""
	})
	return pid
}
