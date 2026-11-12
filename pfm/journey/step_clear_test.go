//go:build journey

package journey

import (
	"fmt"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// clearChat: the owner types /clear; the pane empties to the box, and the same engine goes on under a
// new conversation: the pane's seat now names it, pfm lists it once, and the old conversation is
// listed nowhere (hidden, not left as a second row).
func clearChat(c *harness.Ctx, s State) State {
	term := c.Terminal(s.Term)
	old := s.Chat
	term.Type("/clear\r")
	term.WaitGone("⏺ ", 20*time.Second)
	term.WaitFor("❯")
	sock := herdrSocket(s)
	c.Poll("one live chat on "+s.Pane+" under a conversation after "+old.Session, 20*time.Second,
		func() (bool, string) {
			rows, listed := liveChats(c, sock)
			seats := liveSeats(c, sock)
			seen := fmt.Sprintf("chats %v (listed %v), seats %v", rows, listed, seats)
			if len(rows) != 1 || len(listed) != 1 || len(seats) != 1 {
				return false, seen
			}
			for session, seat := range seats {
				if _, ok := rows[session]; !ok || session == old.Session || seat != [2]string{old.Seat, s.Pane} {
					return false, seen
				}
				s.Chat.Session = session
			}
			return true, seen
		})
	if rows := listRows(c, old.Session); len(rows) != 0 {
		c.Failf("the cleared conversation %s is still listed:\n%s", old.Session, strings.Join(rows, "\n"))
	}
	// The engine's argv still names the conversation it started with, so the scoped fleet check, which
	// reads an engine's session from its argv, waits for the reload after this step.
	if p, ok := processes(c)[old.Engine]; !ok || p.Engine != "claude" {
		c.Failf("/clear replaced the engine: pid %d is gone (%q)", old.Engine, p.Args)
	}
	return s
}

// listRows is every row of `pfm ls --tsv` whose id is session, live or not.
func listRows(c *harness.Ctx, session string) []string {
	var rows []string
	for _, line := range strings.Split(read(c, "pfm", "ls", "--tsv"), "\n") {
		if f := strings.Split(line, "\t"); len(f) > 1 && f[1] == session {
			rows = append(rows, line)
		}
	}
	return rows
}
