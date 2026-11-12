//go:build journey

package journey

import (
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// doctorRows are the rows pfm doctor prints on this machine after the day's chats ended and were
// killed: the plugins it installed read OK, Herdr's ping reads OK at the path the owner's PATH finds
// ({herdr}: /usr/local/bin/herdr on the Linux machine, the pinned bin directory on a CI runner), the
// indicator painter is not stale and the crumb audit finds no invalid entry.
var doctorRows = []string{
	"doctor: claude_plugins ok",
	"doctor: dep herdr path={herdr} ok",
	"doctor: herdr-indicators: ok process enumeration confirms no stale painter",
}

// doctorMachineFails are the failures the journey machine itself causes, the only ones doctor may
// print: the fake engine sends no API request for the harness-prompt check to capture, and with no
// network the professor marketplace's plugins (a release build's GitHub source) are recorded with no
// files, so the flights plugin holds no tracetree kit. {home} is the owner's home.
var doctorMachineFails = []string{
	"doctor: harness-prompt: CHECK FAILED to run (no API request reached the capture sink) — drift unknown",
	"doctor: harness-prompt: CHECK FAILED to run (no API request reached the capture sink) — drift unknown",
	`doctor: tracetree-kit: FAIL need=kit checked={home}/.cc/1/plugins/cache/flights@professor/skills/tracetree/tracetree.py ` +
		`error="stat {home}/.cc/1/plugins/cache/flights@professor/skills/tracetree/tracetree.py: no such file or directory" ` +
		`— fix: run pfm install --yes to refresh the flights plugin copy`,
	`doctor: tracetree-kit: FAIL need=run checked=python3 {home}/.cc/1/plugins/cache/flights@professor/skills/tracetree/tracetree.py ` +
		`error="not run: python3 or the kit failed above" — fix: fix the rows above, then rerun pfm doctor`,
}

// doctor: the owner types pfm doctor in the clone; it prints no failure beyond the machine's own
// (doctorMachineFails), the rows of the day's plugins, Herdr and its painter read OK, the crumb audit
// finds every entry valid, the host check leaves only the owner's own file unclassified, and it exits 3
// for the machine's failures alone.
func doctor(c *harness.Ctx, s State) State {
	c.Expect(`"host":"platform.claude.com"`)
	term := c.Terminal("doctor")
	term.WaitFor(prompt)
	out := filepath.Join(s.Home, "doctor.out")
	code := shellRun(term, "doctor", "clear && (cd ~/.professor && pfm doctor) > ~/doctor.out 2>&1", 120*time.Second)
	lines := strings.Split(read(c, "cat", out), "\n")
	var failed, unclassified, crumbs []string
	for _, line := range lines {
		switch {
		case strings.Contains(line, " FAIL"):
			failed = append(failed, line)
		case strings.HasPrefix(line, "host-check: WARN unclassified "):
			unclassified = append(unclassified, line)
		case strings.HasPrefix(line, "doctor: crumbs "):
			crumbs = append(crumbs, line)
		}
	}
	want := strings.ReplaceAll(strings.Join(doctorMachineFails, "\n"), "{home}", s.Home)
	if got := strings.Join(failed, "\n"); got != want {
		c.Failf("pfm doctor failure rows (%s):\n%s\nwant only the machine's own:\n%s", out, got, want)
	}
	herdr := strings.TrimSpace(read(c, "sh", "-c", "command -v herdr"))
	for _, row := range append(doctorRows, "doctor: failures=2") {
		row = strings.ReplaceAll(row, "{herdr}", herdr)
		if !slices.Contains(lines, row) {
			c.Failf("pfm doctor printed no row %q (%s)", row, out)
		}
	}
	keep := "host-check: WARN unclassified " + filepath.Join(s.Home, ".claude", "owner-keep.txt") +
		" — UNCLASSIFIED — on neither the shared nor the per-account list"
	if !slices.Equal(unclassified, []string{keep}) {
		c.Failf("pfm doctor unclassified rows %q, want only the owner's own file %q (%s)", unclassified, keep, out)
	}
	if len(crumbs) != 1 || !strings.HasSuffix(crumbs[0], " invalid=0") {
		c.Failf("pfm doctor crumb rows %q, want one ending invalid=0 (%s)", crumbs, out)
	}
	if code != 3 {
		c.Failf("pfm doctor exited %d, want 3 (the machine's failures alone) (%s)", code, out)
	}
	return s
}
