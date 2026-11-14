//go:build journey

package journey

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// pinUpdated: the owner first keeps the project's file apart from the upstream change on record (`pfm
// update pin {file} --keep {reason}`, keepUpdated). Then he ports the upstream line into the project's
// file by hand, drops the keep (`--unkeep`) and types `pfm update pin {file}`, the verb the UPDATED row
// names; the file is the new template under its
// scaffold marker, its pin moves to the clone's commit and the new template, and the report is clean.
func pinUpdated(c *harness.Ctx, s State) State {
	term := c.Terminal("adopter")
	pin := adopterBaseline(c, s.Project).Files[movedLocal].PinnedSHA
	head := cloneHead(c, s)
	keepUpdated(c, s, term, pin, head)
	if code := shellRun(term, "port", "printf '%s\\n' '"+movedLine+"' >>"+movedLocal, stepBound); code != 0 {
		c.Failf("porting the upstream line exited %d:\n%s", code, term.Screen())
	}
	if code := shellRun(term, "unkeep", "pfm update pin "+movedLocal+" --unkeep", stepBound); code != 0 {
		c.Failf("pfm update pin --unkeep exited %d:\n%s", code, term.Screen())
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

// keptReason is why the owner keeps the project's file apart from upstream.
const keptReason = "the journey's adopter keeps its own wording"

// keepUpdated: `pfm update pin {movedLocal} --keep {keptReason}` records the keep; the baseline's pin
// carries it from the old pin, at the clone's commit, and the report is clean with the file KEPT.
func keepUpdated(c *harness.Ctx, s State, term *harness.Terminal, pin, head string) {
	out, errs, code := typedRun(c, term, "keep", "pfm update pin "+movedLocal+" --keep "+shellQuote(keptReason))
	want := "kept 1 file(s) apart from upstream at " + head + ": " + keptReason + "\n"
	if code != 0 || out != want || errs != "" {
		c.Failf("pfm update pin --keep exited %d, stdout %q, stderr %q; want exit 0 and %q", code, out, errs, want)
	}
	got := adopterBaseline(c, s.Project).Files[movedLocal]
	today := time.Now().Format(time.DateOnly)
	if got.Kept == nil || got.Kept.Reason != keptReason || got.Kept.At != today || got.Kept.FromSHA != pin ||
		got.PinnedSHA != head || got.TemplateHash != templateHash(c, s, movedTemplate) {
		c.Failf("after pfm update pin --keep, %s's pin %+v (kept %+v); want it at %s kept from %s on %s for %q",
			movedLocal, got, got.Kept, head, pin, today, keptReason)
	}
	kept := fmt.Sprintf("  KEPT          1   (deliberate local divergence, recorded; not review)\n"+
		"    %s   %s  kept %s: %s\n"+
		"      untaken upstream change: git -C %s diff %s %s -- templates/%s\n"+
		"      ported the untaken change after all? pfm update pin %s --unkeep\n",
		movedLocal, movedTemplate, today, keptReason, professorClone(s), pin, head, movedTemplate, movedLocal)
	report, code := adopterReport(c, s.Project)
	files := len(adopterBaseline(c, s.Project).Files)
	if want := reportHead(s.Project, head, head, reportRows{current: files, ignored: 1, kept: kept}) +
		"clean\n"; code != 0 || report != want {
		c.Failf("pfm doctor --project-updates after the keep (exit %d, want 0):\n%s\nwant:\n%s", code, report, want)
	}
}
