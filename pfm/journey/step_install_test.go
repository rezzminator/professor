//go:build journey

package journey

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// doctorFail is a doctor row that fails the host.
var doctorFail = regexp.MustCompile(`(?m)^(.*[^[:alnum:]])?FAIL([^[:alnum:]].*)?$`)

// install: the owner runs INSTALL.md's source install against the snapshot of the tree under test
// from a tool shell whose SHELL is bash, and sees its summary; Herdr's shell wrapper keeps the account's
// login shell, the clone's template skills are linked where Codex reads them; pfm reports the
// snapshot's release; he turns the theme sync on, and pfm's
// services run.
func install(c *harness.Ctx, s State) State {
	// The machine has no sudo for the managed-settings write, and no network for the price table.
	expectInstallErrors(c)
	term := c.Terminal("plain")
	term.WaitFor(prompt)
	if code := shellRun(
		term,
		"install",
		// From a tool shell: SHELL names bash, not the owner's login shell.
		"SHELL=/bin/bash bash -euo pipefail ~/.local/share/journey/install.sh",
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
	login := read(c, "sh", "-c", `printf %s "$SHELL"`)
	wrapper := filepath.Join(s.Home, ".local", "share", "pfm", "install", "bin", "pfm-herdr-shell")
	if want := "pfm_real_shell='" + login + "'"; login == "/bin/bash" ||
		!slices.Contains(strings.Split(read(c, "cat", wrapper), "\n"), want) {
		c.Failf("Herdr's shell wrapper %s names no line %q: an install from a bash tool shell must keep the "+
			"account's login shell %s", wrapper, want, login)
	}
	skill := filepath.Join(s.Home, ".agents", "skills", "dude")
	if got, want := strings.TrimSpace(read(c, "readlink", skill)),
		filepath.Join(professorClone(s), "templates", "global", "skills", "dude"); got != want {
		c.Failf("%s links to %q, want the clone's template skill %q, where Codex reads it", skill, got, want)
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
	themeOn(c, term)
	if got := services(c); got != installedServices {
		c.Failf("pfm's services after the install and the theme on:\n%s\nwant:\n%s", got, installedServices)
	}
	return s
}

// themeOn: the owner turns pfm's theme sync on (`pfm theme on`), the step that links pfm's Herdr plugin,
// whose painter marks each chat's Herdr tab with its state; no Herdr server runs yet, so Herdr's
// surface is applied with its reload left to the server's start.
func themeOn(c *harness.Ctx, term *harness.Terminal) {
	out, errs, code := typedRun(c, term, "theme-on", "pfm theme on --json")
	var report struct {
		Surfaces []struct {
			Surface, Verdict, Detail string
		} `json:"surfaces"`
	}
	herdr := "(no herdr row)"
	if err := json.Unmarshal([]byte(out), &report); err == nil {
		for _, row := range report.Surfaces {
			if row.Surface == "herdr" {
				herdr = row.Verdict + " " + row.Detail
			}
		}
	}
	if want := "APPLIED applies when Herdr starts"; code != 0 || errs != "" || herdr != want {
		c.Failf("pfm theme on: exit %d, herdr row %q, stderr %q; want exit 0, herdr row %q, no stderr:\n%s",
			code, herdr, errs, want, out)
	}
}
