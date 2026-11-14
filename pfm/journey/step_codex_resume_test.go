//go:build journey

package journey

import (
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// codexResume: in the pane its ended Codex chat left, the owner leaves the picker and types `pfm chat
// open {thread}`; the fake Codex comes back on the same thread, pfm lists it as the one live chat on the
// socket and reads it idle, not busy, and `pfm chat history {thread}` lists the turn of the earlier
// session from its Codex rollout.
func codexResume(c *harness.Ctx, s State) State {
	term := c.Terminal(s.Term)
	term.Press(harness.Esc)
	term.WaitFor(prompt)
	term.Type("pfm chat open " + s.Chat.Session + "\r")
	term.WaitFor("Ask Codex to do anything", 20*time.Second)
	c.Poll("thread "+s.Chat.Session+" the one live Codex chat on "+herdrSocket(s), 20*time.Second,
		func() (bool, string) {
			rows, _ := liveChats(c, herdrSocket(s))
			seats := liveSeats(c, herdrSocket(s))
			_, seated := seats[s.Chat.Session]
			return len(rows) == 1 && rows[s.Chat.Session][0] == "live-codex" && len(seats) == 1 && seated,
				fmt.Sprintf("chats %v, seats %v", rows, seats)
		})
	s.Chat.Seat, s.Pane = liveSeats(c, herdrSocket(s))[s.Chat.Session][0], liveSeats(c, herdrSocket(s))[s.Chat.Session][1]
	s.Chat.Engine = engineOf(c, s)
	c.Poll("thread "+s.Chat.Session+" idle in pfm's status", stepBound, func() (bool, string) {
		r := c.Read("pfm", "chat", "status", s.Chat.Session, "--json")
		var status struct {
			State string `json:"state"`
		}
		return r.Code == 0 && json.Unmarshal([]byte(r.Stdout), &status) == nil && status.State == "idle",
			r.Stdout + r.Stderr
	})
	history := c.Read("pfm", "chat", "history", s.Chat.Session)
	turns := historyTurn.FindAllStringSubmatch(history.Stdout, -1)
	if history.Code != 0 || len(turns) < 2 {
		c.Failf("pfm chat history %s exited %d with turns %q, want the earlier session's turn; stdout %q stderr %q",
			s.Chat.Session, history.Code, turns, history.Stdout, history.Stderr)
	}
	asked, answered := turns[len(turns)-2], turns[len(turns)-1]
	sent := regexp.MustCompile(`^a turn mock-engine: \{"type":"turn","reply":"([^"]+)"\}$`).FindStringSubmatch(asked[2])
	if asked[1] != "user" || sent == nil || answered[1] != "assistant" || answered[2] != sent[1] {
		c.Failf("pfm chat history %s ends %q then %q, want the typed turn prompt then its reply", s.Chat.Session,
			asked[1:], answered[1:])
	}
	fleetConsistent(c, s)
	return s
}

// historyTurn is one message of `pfm chat history`: its role and its text.
var historyTurn = regexp.MustCompile(`(?m)^─{5} \S+ · (\w+) ─{5}\n(.*)$`)
