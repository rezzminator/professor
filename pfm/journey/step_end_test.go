//go:build journey

package journey

import (
	"fmt"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// end: the owner types /exit in the branch's chat; Herdr shows pfm's picker again in a fresh tab, no mark
// of the chat on it, and no live chat, seat, or engine process of that chat is left.
func end(c *harness.Ctx, s State) State {
	term := c.Terminal(s.Term)
	term.Type("/exit\r")
	term.WaitFor("New chat", 20*time.Second)
	c.Poll("chat "+s.Chat.Session+" gone from the chats, the seats and the engines", stepBound,
		func() (bool, string) {
			rows, _ := liveChats(c, herdrSocket(s))
			seats := liveSeats(c, herdrSocket(s))
			engine := ""
			if p, ok := processes(c)[s.Chat.Engine]; ok {
				engine = p.Args
			}
			seen := fmt.Sprintf("chats %v, seats %v, engine %q", rows, seats, engine)
			return len(rows) == 0 && len(seats) == 0 && engine == "", seen
		})
	chatTabGone(c, s)
	fleetConsistent(c, s)
	return s
}
