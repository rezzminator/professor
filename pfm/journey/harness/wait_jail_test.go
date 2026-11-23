//go:build jail

package harness

import (
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testwait"
)

func TestPollWaitsForItsConditionAndAMissNamesWhatItSaw(t *testing.T) {
	out, failed, parent := inChild(t, func(t *testing.T) {
		Run(t, Do("root", func(c *Ctx, in int) int {
			tries := 0
			c.Poll("the third look", func() (bool, string) { tries++; return tries == 3, "" })
			c.deadline = spentDeadline
			c.Poll("the flag", func() (bool, string) { return false, "not yet" })
			return in
		}), 0, Hooks{})
	})
	if !parent {
		return
	}
	if got := lineAfter(
		out,
		"root: waited",
	); got != "root: waited for the flag, past the hung-only limit; last seen:" ||
		!failed {
		t.Fatalf("poll miss line %q failed=%v, want the flag named and the child failed\n%s", got, failed, out)
	}
	if got := lineAfter(out, "not yet"); got != "not yet" {
		t.Fatalf("poll miss did not print what it last saw\n%s", out)
	}
}

func TestControlFailsTheStepNamingTheCommandNoEngineTook(t *testing.T) {
	out, failed, parent := inChild(t, func(t *testing.T) {
		Run(t, Do("root", func(c *Ctx, in int) int {
			took := false
			c.Control("an engine to take a look", func(testing.TB) { took = true })
			if !took {
				c.Failf("the control call never ran")
			}
			c.deadline = spentDeadline
			c.Control("an engine to fire its next wait", func(t testing.TB) {
				testwait.Until(t, "a reader on the FIFO", func() (bool, string) { return false, "no reader" })
			})
			return in
		}), 0, Hooks{})
	})
	if !parent {
		return
	}
	if got := lineAfter(
		out,
		"root: waited",
	); got != "root: waited for an engine to fire its next wait, past the hung-only limit" ||
		!failed {
		t.Fatalf("control miss line %q failed=%v, want the command named and the child failed\n%s", got, failed, out)
	}
}
