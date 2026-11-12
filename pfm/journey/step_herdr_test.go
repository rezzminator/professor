//go:build journey

package journey

import (
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// stepBound bounds a step's poll for the state its action leaves.
const stepBound = 10 * time.Second

// herdr: the owner starts Herdr in a plain terminal; its first pane opens on pfm's picker, Herdr lists
// that one pane, and herdr-shield holds it.
func herdr(c *harness.Ctx, s State) State {
	term := c.Terminal("herdr")
	term.WaitFor(prompt)
	term.Type("herdr\r")
	term.WaitFor("New chat", 20*time.Second)
	panes := slices.Sorted(maps.Keys(herdrPanes(c)))
	if len(panes) != 1 {
		c.Failf("Herdr lists panes %q, want exactly one", panes)
	}
	s.Pane = panes[0]
	c.Poll("a live herdr-shield holder on pane "+s.Pane, stepBound, func() (bool, string) {
		holders := liveHolders(c, s)
		for _, h := range holders {
			if h.Pane == s.Pane {
				s.Holder = h.ID
				return true, ""
			}
		}
		return false, fmt.Sprintf("%+v", holders)
	})
	return s
}
