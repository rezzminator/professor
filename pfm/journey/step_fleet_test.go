//go:build journey

package journey

import (
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// fleet: after the day, every branch done, the owner lists every chat in a plain terminal and sees the
// list's header; underneath, the whole machine is consistent (fleetWhole).
func fleet(c *harness.Ctx, s State) State {
	term := c.Terminal("plain")
	if code := shellRun(term, "chats", "clear && pfm chat ls --all", 20*time.Second); code != 0 {
		c.Failf("pfm chat ls --all exited %d:\n%s", code, term.Screen())
	}
	term.WaitFor("live chats everywhere (name · session · state · herdr · dir · last activity):")
	fleetWhole(c)
	return s
}
