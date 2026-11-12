//go:build journey

package journey

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// ownerKeep is the file infra/fence/journey/prepare.sh leaves in ~/.claude before pfm is installed.
const ownerKeep = "the owner wrote this before pfm was installed\n"

// ownerKeepMember is the value of the member prepare.sh writes into ~/.claude/settings.json before pfm
// is installed.
const ownerKeepMember = "the owner set this before pfm was installed"

// uninstall: the owner stops Herdr and uninstalls pfm, and sees its summary; pfm's services are gone
// and the file the owner kept in ~/.claude and the owner's Claude settings are as they were.
func uninstall(c *harness.Ctx, s State) State {
	term := c.Terminal("plain")
	term.WaitFor(prompt)
	if slices.ContainsFunc(herdrSessions(c), func(r State) bool { return r.Session == "" }) {
		if code := shellRun(term, "herdr-stop", "herdr server stop", 20*time.Second); code != 0 {
			c.Failf("herdr server stop exited %d:\n%s", code, term.Screen())
		}
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
	settings := read(c, "cat", filepath.Join(s.Home, ".claude", "settings.json"))
	var member struct {
		OwnerKeep *string `json:"ownerKeep"`
	}
	if err := json.Unmarshal(
		[]byte(settings),
		&member,
	); err != nil || member.OwnerKeep == nil ||
		*member.OwnerKeep != ownerKeepMember {
		c.Failf(
			"~/.claude/settings.json after the uninstall (%v) = %q, want JSON whose ownerKeep is %q",
			err,
			settings,
			ownerKeepMember,
		)
	}
	return s
}
