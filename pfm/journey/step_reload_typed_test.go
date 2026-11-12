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
	// pfm's hook blocks the typed prompt and says why: the reboot is scheduled, not refused.
	term.WaitScreen("the hook's scheduled-reload row", 20*time.Second, func(screen string) bool {
		return strings.Contains(unwrapped(screen), reloadScheduled)
	})
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

// reloadScheduled opens the row the fake Claude draws for pfm's block of a typed /reload, the reload's own
// confirmation in parentheses after it.
const reloadScheduled = "⎿ Blocked by hook: reload scheduled — this chat reboots when the current turn ends ("

// reloadStashed: the owner types a draft and parks it with Ctrl-S, Claude's stash, leaving the composer empty,
// then types /reload (reloadTypedDrawn): the reload never presses Ctrl-S on the empty composer, which would
// bring the parked draft back ahead of its /exit, and no prompt of the chat ever carries the draft.
func reloadStashed(c *harness.Ctx, s State) State {
	term := c.Terminal(s.Term)
	draft := c.Unique("stash-me")
	term.Type(draft)
	term.WaitFor("❯ " + draft)
	term.Press(harness.Ctrl('s'))
	term.WaitGone(draft, stepBound)
	s = reloadTypedDrawn(c, s)
	for _, text := range userTexts(c, transcriptOf(c, s)) {
		if strings.Contains(text, draft) {
			c.Failf(
				"the reloaded chat's transcript %s holds a prompt with the stashed draft: %q",
				transcriptOf(c, s),
				text,
			)
		}
	}
	return s
}
