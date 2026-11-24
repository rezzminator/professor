//go:build jail

package harness

import (
	"errors"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testwait"
)

func TestClosingATerminalEndsItsShell(t *testing.T) {
	for _, row := range []struct {
		name, typed, ready string
		// spent closes the terminal with its patience already used up, so the kill path runs at once.
		spent bool
	}{
		{"the hangup ends it", "echo up\r", "up\nREADY$", false},
		{
			"a foreground job ignoring the hangup is killed", "trap '' HUP; echo sleeping; " + testwait.Hold + "\r",
			"\nsleeping", true,
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			pid := 0
			Run(t, Do("root", func(c *Ctx, in int) int {
				term := c.Terminal("sh")
				term.WaitFor("READY$")
				term.Type(row.typed)
				term.WaitFor(row.ready)
				pid = term.Pid()
				if row.spent {
					c.deadline = spentDeadline
				}
				return in
			}), 0, bashHooks(t))
			if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
				t.Fatalf("shell pid %d after close: kill -0 = %v, want ESRCH", pid, err)
			}
		})
	}
}
