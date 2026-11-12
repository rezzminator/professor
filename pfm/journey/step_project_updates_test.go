//go:build journey

package journey

import (
	"fmt"
	"strings"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// projectUpdates: the owner types `pfm doctor --project-updates` in the project and reads REVIEW
// REQUIRED; underneath, the report holds exactly one row, UPDATED for the template upstream moved,
// with the clone's commit and the upstream diff, and every other count is zero.
func projectUpdates(c *harness.Ctx, s State) State {
	term := c.Terminal("adopter")
	if code := shellRun(term, "report", "pfm doctor --project-updates", stepBound); code != 1 {
		c.Failf("pfm doctor --project-updates exited %d, want 1 (review required):\n%s", code, term.Screen())
	}
	const terminal = "REVIEW REQUIRED — 1 items; nothing was written."
	if !strings.Contains(term.Screen(), terminal) {
		c.Failf("the report's screen does not end %q:\n%s", terminal, term.Screen())
	}
	clone := professorClone(s)
	pin := adopterBaseline(c, s.Project).Files[movedLocal].PinnedSHA
	head := cloneHead(c, s)
	diff := read(c, "git", "-C", clone, "diff", "--no-ext-diff", "--no-textconv", "--no-color", pin, "--",
		"templates/"+movedTemplate)
	var added []string
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			added = append(added, line)
		}
	}
	if len(added) != 1 || added[0] != "+"+movedLine {
		c.Failf("the clone's own diff since the pin adds %q, want only %q", added, "+"+movedLine)
	}
	var row strings.Builder
	fmt.Fprintf(&row, "    %s   %s  pinned @%s\n", movedLocal, movedTemplate, pin)
	fmt.Fprintf(&row, "      upstream change: git -C %s diff %s -- templates/%s\n", clone, pin, movedTemplate)
	row.WriteString("      upstream commits that changed it since the pin, oldest first — port each one's intent:\n")
	fmt.Fprintf(&row, "        %s %s\n", head, movedSubject)
	for _, line := range strings.Split(strings.TrimRight(diff, "\n"), "\n") {
		fmt.Fprintf(&row, "      %s\n", line)
	}
	fmt.Fprintf(&row, "      port what applies into %s, keep the project's own edits, then: pfm update pin %s\n",
		movedLocal, movedLocal)
	fmt.Fprintf(
		&row,
		"      or keep your version on purpose, on record: pfm update pin %s --keep \"<what you did not take, and why>\"\n",
		movedLocal,
	)
	pins := len(adopterBaseline(c, s.Project).Files)
	report, code := adopterReport(c, s.Project)
	want := reportHead(s.Project, pin, head, reportRows{current: pins - 1, ignored: 1, updated: []string{row.String()}})
	if code != 1 || !strings.HasPrefix(report, want) || !strings.HasSuffix(report, "\n"+terminal+"\n") {
		c.Failf(
			"pfm doctor --project-updates (exit %d, want 1):\n%s\nwant it to start:\n%s\nand end %q",
			code,
			report,
			want,
			terminal,
		)
	}
	return s
}

// adopterReport is `pfm doctor --project-updates` read in the project, with its exit code; any stderr
// fails the step.
func adopterReport(c *harness.Ctx, dir string) (string, int) {
	r := c.Read("sh", "-c", `cd "$1" && exec pfm doctor --project-updates`, "sh", dir)
	if r.Stderr != "" {
		c.Failf("pfm doctor --project-updates wrote to stderr (exit %d): %s", r.Code, r.Stderr)
	}
	return r.Stdout, r.Code
}

// reportRows is what one `pfm doctor --project-updates` holds: the current and ignored counts and the
// rows under UPDATED, NEW and RETIRED-NAME, each row whole; every other status is zero.
type reportRows struct {
	current, ignored      int
	updated, new, retired []string
}

// reportHead is the report's head through its RETIRED-NAME rows.
func reportHead(root, from, to string, r reportRows) string {
	var b strings.Builder
	fmt.Fprintf(&b, "professor: %s  blueprint %s → %s\n", root, from, to)
	count := func(status string, n int) { fmt.Fprintf(&b, "  %-13s %d\n", status, n) }
	rows := func(status string, rows []string) {
		count(status, len(rows))
		b.WriteString(strings.Join(rows, ""))
	}
	count("current", r.current)
	count("ignored", r.ignored)
	rows("UPDATED", r.updated)
	rows("NEW", r.new)
	for _, status := range []string{"GONE-UPSTREAM", "MOVED", "LOCAL-DELETED"} {
		count(status, 0)
	}
	rows("RETIRED-NAME", r.retired)
	return b.String()
}
