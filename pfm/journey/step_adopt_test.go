//go:build journey

package journey

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// The templates pfm init leaves for the owner to settle, as docs/SETUP.md closes them: the testing
// manual it materializes by hand, and the global settings template nothing installs into a project.
const (
	manualTemplate = "project/commands/per-project/testing-manual.md"
	manualLocal    = ".claude/commands/testing-manual.md"
	globalTemplate = "project/settings-global.json"
)

// adopt: right after pfm init the owner's report holds three NEW rows; the owner settles them the way
// docs/SETUP.md says: `pfm update adopt` pins the owner's own file on a scaffold path as it stands, the
// testing manual copied in is pinned with `pfm update pin --template`, the global settings template
// is ignored. The owner's file is as it was, the baseline holds the two pins and the ignore, and the
// report ends clean.
func adopt(c *harness.Ctx, s State) State {
	term := c.Terminal("adopter")
	const review = "REVIEW REQUIRED — 3 items; nothing was written."
	if code := shellRun(term, "settle", "pfm doctor --project-updates", stepBound); code != 1 ||
		!strings.Contains(term.Screen(), review) {
		c.Failf("pfm doctor --project-updates exited %d, want 1 and %q on the screen:\n%s", code, review, term.Screen())
	}
	baseline := adopterBaseline(c, s.Project)
	head, pins := baseline.Blueprint.SHA, len(baseline.Files)
	var rows []string
	for _, template := range []string{manualTemplate, rumdlTemplate, globalTemplate} {
		rows = append(rows, "    "+template+" — adopt: copy/adapt it locally, then pfm update pin --template "+
			template+" <local> — or ignore\n")
	}
	report, code := adopterReport(c, s.Project)
	if want := reportHead(s.Project, head, head, reportRows{current: pins, new: rows}); code != 1 ||
		!strings.HasPrefix(report, want) || !strings.HasSuffix(report, "\n"+review+"\n") {
		c.Failf("pfm doctor --project-updates after pfm init (exit %d, want 1):\n%s\nwant it to start:\n%s\nand end %q",
			code, report, want, review)
	}
	for _, typed := range []struct{ label, command, says string }{
		{"adopt", "pfm update adopt", "adopted 1 file(s) at " + head},
		{"manual", `cp "$HOME/.professor/templates/` + manualTemplate + `" ` + manualLocal +
			" && pfm update pin --template " + manualTemplate + " " + manualLocal, "pinned 1 file(s) at " + head},
		{"ignore", "pfm update ignore " + globalTemplate, "ignored 1 template(s)"},
	} {
		if code := shellRun(term, typed.label, typed.command, stepBound); code != 0 ||
			!strings.Contains(term.Screen(), typed.says) {
			c.Failf("%s exited %d, want 0 and %q on the screen:\n%s", typed.command, code, typed.says, term.Screen())
		}
	}
	if got := read(c, "cat", filepath.Join(s.Project, rumdlLocal)); got != adopterRumdl {
		c.Failf("%s after pfm update adopt = %q, want the owner's %q", rumdlLocal, got, adopterRumdl)
	}
	baseline = adopterBaseline(c, s.Project)
	for local, template := range map[string]string{rumdlLocal: rumdlTemplate, manualLocal: manualTemplate} {
		pin, want := baseline.Files[local], templateHash(c, s, template)
		if pin.Template != template || pin.TemplateHash != want || pin.PinnedSHA != head || pin.Kept != nil {
			c.Failf("%s's pin = %+v, want %s at %s, templateHash %s, no keep", local, pin, template, head, want)
		}
	}
	if len(baseline.Files) != pins+2 || !slices.Equal(baseline.Ignored, []string{globalTemplate}) {
		c.Failf("baseline.json: %d pins and ignored %q; want %d pins and ignored [%s]",
			len(baseline.Files), baseline.Ignored, pins+2, globalTemplate)
	}
	report, code = adopterReport(c, s.Project)
	if want := reportHead(s.Project, head, head, reportRows{current: pins + 2, ignored: 1}) + "clean\n"; code != 0 ||
		report != want {
		c.Failf("pfm doctor --project-updates after settling (exit %d, want 0):\n%s\nwant:\n%s", code, report, want)
	}
	return s
}
