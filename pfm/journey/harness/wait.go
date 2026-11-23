package harness

import (
	"context"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/testwait"
)

// Poll waits on the wait door until cond holds; one still unmet past the door's hung-only limit fails the
// step with what was awaited and the last thing cond saw.
func (c *Ctx) Poll(what string, cond func() (ok bool, seen string)) {
	c.t.Helper()
	from, seen := c.now(), ""
	met := c.door(c.deadline, func(t testing.TB) {
		testwait.Until(t, what, func() (bool, string) {
			ok, last := cond()
			seen = last
			return ok, last
		})
	})
	c.noteWait(what, from, met)
	if !met {
		c.Failf("waited for %s, past the hung-only limit; last seen:\n%s", what, seen)
	}
}

// Control runs call, one command to a fake engine's control FIFO (pfm/internal/mockengine/control/controltest),
// on the wait door: a command no engine took within the door's hung-only limit fails the step with what was
// awaited.
func (c *Ctx) Control(what string, call func(t testing.TB)) {
	c.t.Helper()
	from := c.now()
	met := c.door(c.deadline, call)
	c.noteWait(what, from, met)
	if !met {
		c.Failf("waited for %s, past the hung-only limit", what)
	}
}

// door runs wait, one call of the wait door (pfm/internal/testwait), on this step's goroutine under
// deadline, and reports whether its condition was met: false once the door gave up at its hung-only
// limit. The door fails a wait by Fatalf, which here unwinds to door instead of ending the test, so the
// walker still records the step and blocks its descendants. A step's own Failf inside wait passes through.
func (c *Ctx) door(deadline func() (time.Time, bool), wait func(t testing.TB)) (met bool) {
	defer func() {
		if r := recover(); r != nil {
			if _, missed := r.(doorMiss); !missed {
				panic(r)
			}
			met = false
		}
	}()
	wait(doorT{TB: c.t, deadline: deadline})
	return true
}

// doorMiss unwinds a wait the door gave up on back to Ctx.door.
type doorMiss struct{}

// doorT is the step's test as the wait door sees it: its deadline is the step's, its Fatalf, the
// door's one way of failing a wait, unwinds to Ctx.door, and its Context outlives the test's own end,
// so a Read in a Cleanup (which testing runs after cancelling t.Context) still runs under the door's limit.
type doorT struct {
	testing.TB
	deadline func() (time.Time, bool)
}

func (d doorT) Fatalf(string, ...any)       { panic(doorMiss{}) }
func (d doorT) Deadline() (time.Time, bool) { return d.deadline() }
func (d doorT) Context() context.Context    { return context.WithoutCancel(d.TB.Context()) }

// noteWait records a wait this step made, from its start until now, and whether its condition held.
func (c *Ctx) noteWait(what string, from time.Time, met bool) {
	if c.waited != nil {
		c.waited(WaitTime{Path: c.path, What: what, Took: c.now().Sub(from), Met: met})
	}
}
