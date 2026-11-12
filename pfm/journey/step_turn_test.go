//go:build journey

package journey

import (
	"encoding/json"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// turn: the owner types a prompt in the branch's chat and sees the fake engine's reply under it; pfm
// then reads the chat idle again.
func turn(c *harness.Ctx, s State) State {
	reply := c.Unique("reply")
	typedTurn(c, s, "a turn", `{"type":"turn","reply":"`+reply+`"}`, reply)
	return s
}

// typedTurn types words and the fake engine's directive as one prompt in the branch's chat, waits for
// reply under it, then for pfm to read the chat idle again.
func typedTurn(c *harness.Ctx, s State, words, directive, reply string) {
	term := c.Terminal(s.Term)
	term.Type(words + " mock-engine: " + directive + "\r")
	term.WaitFor("⏺ "+reply, 20*time.Second)
	c.Poll("chat "+s.Chat.Session+" idle in pfm's status", stepBound, func() (bool, string) {
		r := c.Read("pfm", "chat", "status", s.Chat.Session, "--json")
		var status struct {
			State string `json:"state"`
		}
		return r.Code == 0 && json.Unmarshal([]byte(r.Stdout), &status) == nil && status.State == "idle",
			r.Stdout + r.Stderr
	})
}
