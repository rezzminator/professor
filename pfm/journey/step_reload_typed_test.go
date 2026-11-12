//go:build journey

package journey

import (
	"fmt"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// reloadTyped: the owner types /reload in the branch's chat; the chat comes back in the same pane with the same
// seat and session under a new engine process, still the only live chat. The step ends once pfm's
// reload run has confirmed the new engine and exited: an /exit typed while it still polls is swallowed
// for its whole bound (product defect, phase-1 report § 7).
func reloadTyped(c *harness.Ctx, s State) State {
	term := c.Terminal(s.Term)
	term.Type("/reload\r")
	before := s.Chat.Engine
	c.Poll("a new engine for session "+s.Chat.Session, 20*time.Second, func() (bool, string) {
		for pid, session := range engines(processes(c)) {
			if session == s.Chat.Session && pid != before {
				return true, ""
			}
		}
		return false, fmt.Sprintf("engines %v", engines(processes(c)))
	})
	c.Poll("pfm's reload run on "+herdrSocket(s)+" done", 20*time.Second, func() (bool, string) {
		for _, p := range processes(c) {
			if strings.Contains(p.Args, " internal reload-run ") && strings.Contains(p.Args, herdrSocket(s)) {
				return false, fmt.Sprintf("pid %d: %s", p.Pid, p.Args)
			}
		}
		return true, ""
	})
	term.WaitGone("/reload", 20*time.Second)
	term.WaitFor("❯")
	s.Chat.Engine = engineOf(c, s)
	seats := liveSeats(c, herdrSocket(s))
	if got, want := seats[s.Chat.Session], [2]string{s.Chat.Seat, s.Pane}; len(seats) != 1 || got != want {
		c.Failf("live seats after the reload = %v, want only %s on seat %v", seats, s.Chat.Session, want)
	}
	fleetConsistent(c, s)
	return s
}
