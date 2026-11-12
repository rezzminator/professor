//go:build journey

package journey

import (
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// pinUpdated: the owner ports the upstream line into the project's file by hand and types
// `pfm update pin {file}`, the verb the UPDATED row names; the file is the new template under its
// scaffold marker, its pin moves to the clone's commit and the new template, and the report is clean.
func pinUpdated(c *harness.Ctx, s State) State {
	term := c.Terminal("adopter")
	pin := adopterBaseline(c, s.Project).Files[movedLocal].PinnedSHA
	head := cloneHead(c, s)
	if code := shellRun(term, "port", "printf '%s\\n' '"+movedLine+"' >>"+movedLocal, stepBound); code != 0 {
		c.Failf("porting the upstream line exited %d:\n%s", code, term.Screen())
	}
	if code := shellRun(term, "pin", "pfm update pin "+movedLocal, stepBound); code != 0 {
		c.Failf("pfm update pin exited %d:\n%s", code, term.Screen())
	}
	if pinned := "pinned 1 file(s) at " + head; !strings.Contains(term.Screen(), pinned) {
		c.Failf("pfm update pin's screen has no %q:\n%s", pinned, term.Screen())
	}
	template := read(c, "cat", filepath.Join(professorClone(s), "templates", filepath.FromSlash(movedTemplate)))
	marker := "# pfm-scaffold: " + movedTemplate + "@" + pin +
		" — this file is YOURS; upstream deltas arrive via pfm update, reviewed and hand-applied\n"
	if want, got := "---\n"+marker+strings.TrimPrefix(template, "---\n"),
		read(c, "cat", filepath.Join(s.Project, movedLocal)); got != want {
		c.Failf("%s after the port = %q, want the new template under pfm init's marker %q", movedLocal, got, want)
	}
	baseline := adopterBaseline(c, s.Project)
	got, want := baseline.Files[movedLocal], templateHash(c, s, movedTemplate)
	if got.Template != movedTemplate || got.PinnedSHA != head || got.TemplateHash != want || got.Kept != nil ||
		baseline.Blueprint.SHA != head {
		c.Failf(
			"after pfm update pin: blueprint %s, %s's pin %+v; want blueprint %s and a pin of %s at %s, templateHash %s, no keep",
			baseline.Blueprint.SHA,
			movedLocal,
			got,
			head,
			movedTemplate,
			head,
			want,
		)
	}
	report, code := adopterReport(c, s.Project)
	if want := reportHead(
		s.Project,
		head,
		head,
		reportRows{current: len(baseline.Files), ignored: 1},
	) + "clean\n"; code != 0 ||
		report != want {
		c.Failf("pfm doctor --project-updates after the pin (exit %d, want 0):\n%s\nwant:\n%s", code, report, want)
	}
	return s
}
