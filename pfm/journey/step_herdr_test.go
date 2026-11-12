//go:build journey

package journey

import (
	"maps"
	"slices"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// stepBound bounds a step's poll for the state its action leaves.
const stepBound = 10 * time.Second

// herdr: the owner starts Herdr, its default session, in a plain terminal; its first pane opens on
// pfm's picker and Herdr lists that one pane.
func herdr(c *harness.Ctx, s State) State {
	s.Term, s.Project = "herdr", s.Home
	term := c.Terminal(s.Term)
	term.WaitFor(prompt)
	term.Type("herdr\r")
	return firstPane(c, s)
}

// firstPane waits for the picker in the first pane of the branch's Herdr session, then records that
// pane, the only one Herdr lists.
func firstPane(c *harness.Ctx, s State) State {
	c.Terminal(s.Term).WaitFor("New chat", 20*time.Second)
	panes := slices.Sorted(maps.Keys(herdrPanes(c, s)))
	if len(panes) != 1 {
		c.Failf("Herdr lists panes %q, want exactly one", panes)
	}
	s.Pane = panes[0]
	return s
}
