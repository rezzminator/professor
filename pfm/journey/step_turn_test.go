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
	term := c.Terminal(s.Term)
	term.Type(`a turn mock-engine: {"type":"turn","reply":"` + reply + `"}` + "\r")
	term.WaitFor("⏺ "+reply, 20*time.Second)
	c.Poll("chat "+s.Chat.Session+" idle in pfm's status", stepBound, func() (bool, string) {
		r := c.Read("pfm", "chat", "status", s.Chat.Session, "--json")
		var status struct {
			State string `json:"state"`
		}
		return r.Code == 0 && json.Unmarshal([]byte(r.Stdout), &status) == nil && status.State == "idle",
			r.Stdout + r.Stderr
	})
	return s
}
