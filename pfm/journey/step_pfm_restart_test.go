//go:build journey

package journey

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// pfmRestart: with one of pfm's services stopped by hand, the owner previews `pfm restart --dry-run`,
// which plans to end both live chats, to keep that service stopped, and changes nothing, then runs `pfm
// restart`: it ends both chats, verifies every step, leaves the stopped service stopped and the others
// as the install left them, no engine of the chats running, each chat resumable, and the
// branch's Herdr server still up.
func pfmRestart(c *harness.Ctx, s State) State {
	term := c.Terminal("restart-ctl")
	pauseService(c)
	c.Cleanup(func() { resumeService(c) })
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
	keep := fmt.Sprintf("%-14s %s  stays stopped (was not running before: %s)", "services", pausedService, pausedBefore)
	if !slices.Contains(strings.Split(plan.Stdout, "\n"), "keep      "+keep) {
		c.Failf("pfm restart --dry-run plans no %q for the unit stopped before it:\n%s", "keep      "+keep, plan.Stdout)
	}
	if got := services(c); got != pausedServices {
		c.Failf("pfm's services after the dry run:\n%s\nwant:\n%s", got, pausedServices)
	}
	if code := shellRun(term, "restart-run", "clear && pfm restart", 120*time.Second); code != 0 {
		c.Failf("pfm restart exited %d:\n%s", code, term.Screen())
	}
	screen := unwrapped(term.Screen())
	want := []string{
		fmt.Sprintf("restart: ended %d chat(s)", len(s.Chats)), "restart: every step verified", "keep   " + keep,
	}
	for _, chat := range s.Chats {
		want = append(want, fmt.Sprintf("  ended  %-8s %-24s %s", "claude", chat.Name, chat.Session))
	}
	for _, line := range want {
		if !strings.Contains(screen, line) {
			c.Failf("pfm restart printed no %q:\n%s", line, term.Screen())
		}
	}
	if got := services(c); got != pausedServices {
		c.Failf("pfm's services after the restart, which leaves stopped what was stopped:\n%s\nwant:\n%s",
			got, pausedServices)
	}
	resumeService(c)
	if got := services(c); got != installedServices {
		c.Failf("pfm's services after the owner starts %s again:\n%s\nwant:\n%s", pausedService, got, installedServices)
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
