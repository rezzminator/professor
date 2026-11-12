//go:build journey

package journey

import (
	"fmt"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// end: the owner types /exit in the branch's chat; Herdr shows pfm's picker again, and no live chat, seat,
// engine process or holder of that chat is left.
func end(c *harness.Ctx, s State) State {
	term := c.Terminal(s.Term)
	term.Type("/exit\r")
	term.WaitFor("New chat", 20*time.Second)
	c.Poll("chat "+s.Chat.Session+" gone from the chats, the seats, the engines and the holders", stepBound,
		func() (bool, string) {
			rows, _ := liveChats(c, herdrSocket(s))
			seats := liveSeats(c, herdrSocket(s))
			engine := ""
			if p, ok := processes(c)[s.Chat.Engine]; ok {
				engine = p.Args
			}
			holder := false
			for _, h := range liveHolders(c, herdrSocket(s)) {
				holder = holder || h.ID == s.Holder
			}
			seen := fmt.Sprintf(
				"chats %v, seats %v, engine %q, holder %s live %v",
				rows,
				seats,
				engine,
				s.Holder,
				holder,
			)
			return len(rows) == 0 && len(seats) == 0 && engine == "" && !holder, seen
		})
	fleetConsistent(c, s)
	return s
}
