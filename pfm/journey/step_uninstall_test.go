//go:build journey

package journey

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// ownerKeep is the file infra/fence/journey/prepare.sh leaves in ~/.claude before pfm is installed.
const ownerKeep = "the owner wrote this before pfm was installed\n"

// uninstall: the owner stops Herdr and uninstalls pfm, and sees its summary; pfm's services are gone
// and the file the owner kept in ~/.claude is as it was.
func uninstall(c *harness.Ctx, s State) State {
	term := c.Terminal("plain")
	if code := shellRun(term, "herdr-stop", "herdr server stop", 20*time.Second); code != 0 {
		c.Failf("herdr server stop exited %d:\n%s", code, term.Screen())
	}
	if code := shellRun(term, "uninstall", "pfm uninstall", 60*time.Second); code != 0 {
		c.Failf("pfm uninstall exited %d:\n%s", code, term.Screen())
	}
	if screen := term.Screen(); !strings.Contains(screen, "summary changed=") {
		c.Failf("the uninstall's last screen has no summary:\n%s", screen)
	}
	c.Poll("pfm's services gone", stepBound, func() (bool, string) {
		got := services(c)
		return got == "", got
	})
	if got := read(c, "cat", filepath.Join(s.Home, ".claude", "owner-keep.txt")); got != ownerKeep {
		c.Failf("~/.claude/owner-keep.txt = %q after the uninstall, want %q", got, ownerKeep)
	}
	return s
}
