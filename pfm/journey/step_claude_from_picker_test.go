//go:build journey

package journey

import (
	"fmt"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// claudeFromPicker: in Herdr's first pane the owner picks a new Claude chat and walks the wizard with
// Enter; the fake Claude draws in the pane, pfm lists exactly one live Claude chat hosted by Herdr, its
// seat is live on that pane and its engine runs under the pane's holder.
func claudeFromPicker(c *harness.Ctx, s State) State {
	term := c.Terminal("herdr")
	term.Press(harness.Enter)
	term.WaitFor("choose the cache")
	term.Press(harness.Enter)
	term.WaitFor("❯", 20*time.Second)
	c.Poll("one live Claude chat with a live seat on "+s.Pane, 20*time.Second, func() (bool, string) {
		rows, _ := liveChats(c)
		seats := liveSeats(c, s)
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
	c.Poll("one engine of session "+s.Chat.Session+" under holder "+s.Holder, stepBound, func() (bool, string) {
		table := processes(c)
		var found []int
		for p, session := range engines(table) {
			if session == s.Chat.Session {
				found = append(found, p)
			}
		}
		holderPid := 0
		for _, h := range liveHolders(c, s) {
			if h.ID == s.Holder {
				holderPid = h.Pid
			}
		}
		if len(found) != 1 || holderPid == 0 || !under(table, found[0], holderPid) {
			return false, fmt.Sprintf("engines %v, holder pid %d", found, holderPid)
		}
		pid = found[0]
		return true, ""
	})
	return pid
}
