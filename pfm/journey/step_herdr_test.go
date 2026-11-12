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

// herdr: the owner starts Herdr in a plain terminal and its first pane opens on pfm's picker; Herdr
// lists that one pane. It is a session of the branch's own (herdrSession), never the default: a
// default session running beside the plain branches would be the Herdr every plain `pfm chat new`
// on the machine is hosted in, while those branches play the owner with no Herdr running.
func herdr(c *harness.Ctx, s State) State {
	return herdrSession(c, s)
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
