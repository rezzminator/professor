//go:build journey

package journey

import (
	"regexp"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// doctorFail is a doctor row that fails the host.
var doctorFail = regexp.MustCompile(`(?m)^(.*[^[:alnum:]])?FAIL([^[:alnum:]].*)?$`)

// install: the owner runs INSTALL.md's source install against the snapshot of the tree under test
// and sees its summary; pfm reports the snapshot's release, its services run.
func install(c *harness.Ctx, s State) State {
	// The machine has no sudo for the managed-settings write, and no network for the price table.
	c.Expect(`"step":"managed-cleanup /etc/claude-code/managed-settings.d/pfm.json not written"`)
	c.Expect(`"host":"platform.claude.com"`)
	term := c.Terminal("plain")
	term.WaitFor(prompt)
	if code := shellRun(
		term,
		"install",
		"bash -euo pipefail ~/.local/share/journey/install.sh",
		150*time.Second,
	); code != 0 {
		c.Failf("INSTALL.md's install exited %d:\n%s", code, term.Screen())
	}
	if screen := term.Screen(); !strings.Contains(screen, "summary changed=") || doctorFail.MatchString(screen) {
		c.Failf("the install's last screen has no summary, or a FAIL row:\n%s", screen)
	}
	if got, want := read(c, "pfm", "--version"), "pfm "+s.Release+"\n"; got != want {
		c.Failf("pfm --version = %q, want %q", got, want)
	}
	doctor := c.Read("sh", "-c", `cd "$HOME/.professor" && exec pfm doctor`)
	// The fake engine's `plugin install` lays down no plugin copy, so the flights kit is absent.
	want := []string{
		"doctor: tracetree-kit: FAIL need=kit",
		"doctor: tracetree-kit: FAIL need=run",
	}
	rows := doctorFail.FindAllString(doctor.Stdout+doctor.Stderr, -1)
	if len(rows) != len(want) || !strings.HasPrefix(rows[0], want[0]) || !strings.HasPrefix(rows[1], want[1]) {
		c.Failf("pfm doctor (exit %d) FAIL rows = %q, want exactly the two prefixed %q", doctor.Code, rows, want)
	}
	if got := services(c); got != installedServices {
		c.Failf("pfm's services after the install:\n%s\nwant:\n%s", got, installedServices)
	}
	return s
}
