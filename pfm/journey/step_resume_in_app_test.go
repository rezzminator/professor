//go:build journey

package journey

import (
	"fmt"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// resumeInApp: pfm starts a fresh conversation on the pane (`pfm chat reload --new`, no prompt), and the owner
// types `/resume {the chat it left}` inside it. The engine's argv still names the launch session, which never
// got a prompt: pfm lists the resumed chat live on the pane and no `(no prompt)` row for the abandoned launch.
func resumeInApp(c *harness.Ctx, s State) State {
	cli := c.Terminal(s.Term + "-cli")
	cli.WaitFor(prompt)
	left, sock := s.Chat, herdrSocket(s)
	command := "pfm chat reload --new --sock " + sock + " --pane " + s.Pane
	if code := shellRun(cli, "reload-new-bare", command, 30*time.Second); code != 0 {
		c.Failf("%s exited %d, want 0", command, code)
	}
	var launch string
	c.Poll("one live seat on "+s.Pane+" under a launch session after "+left.Session, 20*time.Second,
		func() (bool, string) {
			seats := liveSeats(c, sock)
			for session, seat := range seats {
				if session != left.Session && seat[1] == s.Pane && len(seats) == 1 {
					launch = session
					return true, ""
				}
			}
			return false, fmt.Sprintf("live seats %v", seats)
		})
	// pfm names the launch session before its engine starts: /resume is typed into that engine once it runs.
	launched := s
	launched.Chat.Session = launch
	engine := engineOf(c, launched)
	term := c.Terminal(s.Term)
	term.WaitFor("❯", 20*time.Second)
	term.Type("/resume " + left.Session + "\r")
	c.Poll("the seat of "+s.Pane+" back on "+left.Session, 20*time.Second, func() (bool, string) {
		seats := liveSeats(c, sock)
		return len(seats) == 1 && seats[left.Session][1] == s.Pane, fmt.Sprintf("live seats %v", seats)
	})
	if p, ok := processes(c)[engine]; !ok || p.Engine != "claude" {
		c.Failf("the engine %d that resumed %s in place is gone", engine, left.Session)
	}
	// escape herdr/in-app-resume-ghost-row (d0590b30): the launch thread the pane left keeps no row.
	c.Poll("no row for the abandoned launch "+launch+" and "+left.Session+" live", stepBound, func() (bool, string) {
		ghost, resumed := listRows(c, launch), tsvRow(c, left.Session)
		live := len(resumed) > 0 && resumed[0] == "live-claude"
		return len(ghost) == 0 &&
				live, fmt.Sprintf(
				"launch rows %q, resumed row %q",
				strings.Join(ghost, "\n"),
				resumed,
			)
	})
	s.Chat.Engine = engine
	return s
}
