//go:build journey

package journey

import (
	"fmt"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// codexReload: from a second terminal the owner aims `pfm chat reload` at the branch's Codex chat (its
// Herdr socket and pane); the chat comes back in the same pane with the same seat and thread under a new
// Codex process (`codex resume {thread}`), still the only live chat on the socket.
func codexReload(c *harness.Ctx, s State) State {
	cli := c.Terminal(s.Term + "-cli")
	cli.WaitFor(prompt)
	before := s.Chat.Engine
	command := "pfm chat reload --sock " + herdrSocket(s) + " --pane " + s.Pane
	if code := shellRun(cli, "reload", command, 20*time.Second); code != 0 {
		c.Failf("%s exited %d, want 0", command, code)
	}
	c.Poll("a new Codex resuming thread "+s.Chat.Session, 20*time.Second, func() (bool, string) {
		table := processes(c)
		for pid, p := range table {
			if p.Engine == "codex" && p.Session == s.Chat.Session && pid != before &&
				strings.Contains(p.Args, " resume "+s.Chat.Session) {
				return true, ""
			}
		}
		return false, fmt.Sprintf("engines %v", engines(table))
	})
	reloadRunDone(c, s)
	c.Terminal(s.Term).WaitFor("Ask Codex to do anything", 20*time.Second)
	s.Chat.Engine = engineOf(c, s)
	seats := liveSeats(c, herdrSocket(s))
	if got, want := seats[s.Chat.Session], [2]string{s.Chat.Seat, s.Pane}; len(seats) != 1 || got != want {
		c.Failf("live seats after the reload = %v, want only %s on seat %v", seats, s.Chat.Session, want)
	}
	fleetConsistent(c, s)
	return s
}

// reloadRunDone waits for pfm's reload run on the branch's socket to exit, its new engine confirmed.
func reloadRunDone(c *harness.Ctx, s State) {
	c.Poll("pfm's reload run on "+herdrSocket(s)+" done", 20*time.Second, func() (bool, string) {
		for _, p := range processes(c) {
			if strings.Contains(p.Args, " internal reload-run ") && strings.Contains(p.Args, herdrSocket(s)) {
				return false, fmt.Sprintf("pid %d: %s", p.Pid, p.Args)
			}
		}
		return true, ""
	})
}
