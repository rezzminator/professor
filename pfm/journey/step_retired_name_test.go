//go:build journey

package journey

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/professor"
	"github.com/rezzminator/professor/pfm/internal/retired"
	"github.com/rezzminator/professor/pfm/journey/harness"
)

// The owner's own note naming a retired agent, and why the owner keeps it.
const (
	retiredNote   = "notes.md"
	retiredReason = "an old note, kept as the owner wrote it"
)

// retiredName: the owner writes an old agent name, one the clone's retired list carries, into a note
// of the project's own and types `pfm doctor --project-updates`: the report holds exactly that one
// RETIRED-NAME row. The owner keeps the note with `pfm update accept`; the baseline records the rule
// and the report is clean, the hit counted as accepted.
func retiredName(c *harness.Ctx, s State) State {
	file := filepath.Join(professorClone(s), "templates", "global", "retired.json")
	var list retired.Lists
	if err := json.Unmarshal([]byte(read(c, "cat", file)), &list); err != nil {
		c.Failf("%s: %v", file, err)
	}
	at := slices.IndexFunc(list.Names, func(n retired.Name) bool {
		return n.Kind == retired.KindAgent && n.Successor != ""
	})
	if at < 0 {
		c.Failf("%s names no retired agent with a successor", file)
	}
	old := list.Names[at]
	term := c.Terminal("adopter")
	if code := shellRun(term, "note", "printf '%s\\n' 'Land it with the "+old.Name+" agent.' >"+retiredNote,
		stepBound); code != 0 {
		c.Failf("writing the note exited %d:\n%s", code, term.Screen())
	}
	const review = "REVIEW REQUIRED — 1 items; nothing was written."
	if code := shellRun(term, "retired", "pfm doctor --project-updates", stepBound); code != 1 ||
		!strings.Contains(term.Screen(), review) {
		c.Failf("pfm doctor --project-updates exited %d, want 1 and %q on the screen:\n%s", code, review, term.Screen())
	}
	baseline := adopterBaseline(c, s.Project)
	head, pins := baseline.Blueprint.SHA, len(baseline.Files)
	row := fmt.Sprintf("    %s:1   %s — retired agent, now %s\n", retiredNote, old.Name, old.Successor)
	report, code := adopterReport(c, s.Project)
	if want := reportHead(
		s.Project,
		head,
		head,
		reportRows{current: pins, ignored: 1, retired: []string{row}},
	); code != 1 ||
		!strings.HasPrefix(report, want) ||
		!strings.HasSuffix(report, "\n"+review+"\n") {
		c.Failf(
			"pfm doctor --project-updates (exit %d, want 1):\n%s\nwant it to start:\n%s\nand end %q",
			code,
			report,
			want,
			review,
		)
	}
	if code := shellRun(term, "accept", "pfm update accept "+retiredNote+" --name "+old.Name+
		" --reason '"+retiredReason+"'", stepBound); code != 0 ||
		!strings.Contains(term.Screen(), "accepted retired-name hits under 1 path(s)") {
		c.Failf("pfm update accept exited %d, want 0 and its accepted line:\n%s", code, term.Screen())
	}
	if code := shellRun(term, "accepted", "pfm doctor --project-updates", stepBound); code != 0 {
		c.Failf("pfm doctor --project-updates after the accept exited %d, want 0:\n%s", code, term.Screen())
	}
	baseline = adopterBaseline(c, s.Project)
	rule := professor.RetiredRule{Path: retiredNote, Name: old.Name, Reason: retiredReason}
	if baseline.Version != professor.BaselineDecisionsVersion ||
		!slices.Equal(baseline.RetiredAccepted, []professor.RetiredRule{rule}) {
		c.Failf("baseline.json after the accept: version %d, retiredAccepted %+v; want version %d and only %+v",
			baseline.Version, baseline.RetiredAccepted, professor.BaselineDecisionsVersion, rule)
	}
	report, code = adopterReport(c, s.Project)
	want := reportHead(s.Project, head, head, reportRows{current: pins, ignored: 1}) +
		"  ACCEPTED      1   (retired-name hits accepted by 1 recorded rule(s); not review)\n" +
		fmt.Sprintf("    %s   %s   1 hit(s) — %s\n", retiredNote, old.Name, retiredReason) + "clean\n"
	if code != 0 || report != want {
		c.Failf("pfm doctor --project-updates after the accept (exit %d, want 0):\n%s\nwant:\n%s", code, report, want)
	}
	return s
}
