//go:build journey

package journey

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// pfmRestart: the owner previews `pfm restart --dry-run`, which plans to end both live chats and
// changes nothing, then runs `pfm restart`: it ends both chats, verifies every step, and leaves pfm's
// services as the install left them, no engine of the chats running, each chat resumable, and the
// branch's Herdr server still up.
func pfmRestart(c *harness.Ctx, s State) State {
	term := c.Terminal("restart-ctl")
	plan := c.Read("pfm", "restart", "--dry-run")
	if plan.Code != 0 {
		c.Failf("pfm restart --dry-run exited %d:\n%s%s", plan.Code, plan.Stdout, plan.Stderr)
	}
	for _, chat := range s.Chats {
		line := regexp.MustCompile(`(?m)^stop +\d+ +chats +claude ` + regexp.QuoteMeta(chat.Name) + ` session ` +
			regexp.QuoteMeta(chat.Session) + `$`)
		if !line.MatchString(plan.Stdout) {
			c.Failf("pfm restart --dry-run plans no stop of %s (%s):\n%s", chat.Name, chat.Session, plan.Stdout)
		}
	}
	running := engines(processes(c))
	for _, chat := range s.Chats {
		if running[chat.Engine] != chat.Session {
			c.Failf("after the dry run engine %d of %s is gone: engines %v", chat.Engine, chat.Name, running)
		}
	}
	if got := services(c); got != installedServices {
		c.Failf("pfm's services after the dry run:\n%s\nwant:\n%s", got, installedServices)
	}
	if code := shellRun(term, "restart-run", "clear && pfm restart", 120*time.Second); code != 0 {
		c.Failf("pfm restart exited %d:\n%s", code, term.Screen())
	}
	screen := unwrapped(term.Screen())
	want := []string{fmt.Sprintf("restart: ended %d chat(s)", len(s.Chats)), "restart: every step verified"}
	for _, chat := range s.Chats {
		want = append(want, fmt.Sprintf("  ended  %-8s %-24s %s", "claude", chat.Name, chat.Session))
	}
	for _, line := range want {
		if !strings.Contains(screen, line) {
			c.Failf("pfm restart printed no %q:\n%s", line, term.Screen())
		}
	}
	if got := services(c); got != installedServices {
		c.Failf("pfm's services after the restart:\n%s\nwant:\n%s", got, installedServices)
	}
	for _, chat := range s.Chats {
		c.Poll(chat.Name+" ended and resumable, no engine of it left", stepBound, func() (bool, string) {
			rows := listRows(c, chat.Session)
			for pid, session := range engines(processes(c)) {
				if session == chat.Session {
					return false, fmt.Sprintf("engine %d", pid)
				}
			}
			return len(rows) == 1 && strings.HasPrefix(rows[0], "resume-claude\t"), strings.Join(rows, "\n")
		})
	}
	if herdrServer(processes(c), s) == 0 {
		c.Failf("no Herdr server of session %s after pfm restart", s.Session)
	}
	return s
}
