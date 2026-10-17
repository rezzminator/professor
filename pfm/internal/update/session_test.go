package update

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/professor"
)

// upstreamCommit changes one store template and commits it, as a later
// release does; the fixture adopter stays pinned where it was.
func (f projectUpdateFixture) upstreamCommit(t *testing.T, template, content, subject string) {
	t.Helper()
	writeProjectFixtureFile(t, f.store, "templates/"+template, content)
	gitTemp(t, f.store, "add", "-A")
	gitTemp(t, f.store, "commit", "-qm", subject)
}

// upstreamCommits is count upstreamCommit calls on one template written as one
// git fast-import stream: commit index (1…count) sets template to
// content(index) under subject(index), on the store's branch after its HEAD,
// and the store is checked out at the last, as the calls would leave it.
func (f projectUpdateFixture) upstreamCommits(
	t *testing.T,
	template string,
	count int,
	content, subject func(index int) string,
) {
	t.Helper()
	branch := updateGitBranch(t, f.store)
	parent := updateGitRevision(t, f.store, "HEAD")
	when := time.Now().Unix()
	var stream strings.Builder
	for index := 1; index <= count; index++ {
		message, body := subject(index)+"\n", content(index)
		fmt.Fprintf(&stream, "commit refs/heads/%s\n", branch)
		fmt.Fprintf(&stream, "committer fixture-identity <fixture.invalid> %d +0000\n", when)
		fmt.Fprintf(&stream, "data %d\n%s", len(message), message)
		if index == 1 {
			fmt.Fprintf(&stream, "from %s\n", parent)
		}
		fmt.Fprintf(&stream, "M 100644 inline templates/%s\ndata %d\n%s\n", template, len(body), body)
	}
	command := exec.Command("git", "fast-import", "--quiet")
	command.Dir = f.store
	command.Stdin = strings.NewReader(stream.String())
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git fast-import: %v\n%s", err, output)
	}
	gitTemp(t, f.store, "reset", "-q", "--hard")
}

// sessionVerb runs one `pfm update <verb>` in the fixture project.
func (f projectUpdateFixture) sessionVerb(args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = Run(append(args, "--root", f.project), &out, &errOut, f.runtime)
	return code, out.String(), errOut.String()
}

// sessionReport is the report a session re-runs: the retired-name scan on.
func (f projectUpdateFixture) sessionReport() (code int, stdout string) {
	var out bytes.Buffer
	code = professor.RunProjectUpdates(f.project, f.runtime.Paths.Home, false, &out, ScanRetiredNames)
	return code, out.String()
}

// TestSessionClosesEveryRowWithRecordedDecisions: an adopter pinned releases
// back, with a NEW template, retired-name hits (one real, one in a records
// directory, a URL, a command the project defines itself) and deleted or retired
// files, reaches exit 0 and `clean` through the verbs the report prints, and
// every deliberate skip stays on record in the report.
func TestSessionClosesEveryRowWithRecordedDecisions(t *testing.T) {
	t.Parallel()
	fixture := newProjectUpdateFixture(t)
	fixture.upstreamCommit(t, "project/updated-one.md", "newer one\n", "second change to one")
	fixture.upstreamCommit(t, "project/updated-one.md", "newest one\n", "third change to one")
	writeProjectFixtureFile(t, fixture.project, ".claude/commands/km.md", "the project's own\n")
	writeProjectFixtureFile(t, fixture.project, "docs/dev/backlog/wave.md", "ran /wave:run for the wave\n")
	writeProjectFixtureFile(t, fixture.project, "docs/refs.md", "see https://opencode.ai/docs/agents/ and /km here\n")
	writeProjectFixtureFile(t, fixture.project, "src/notes.txt", "run /jc now\n")

	code, report := fixture.sessionReport()
	if code != 1 {
		t.Fatalf("opening report code=%d:\n%s", code, report)
	}
	for _, want := range []string{
		"RETIRED-NAME  2", "docs/dev/backlog/wave.md:1", "src/notes.txt:1",
		"upstream commits that changed it since the pin, oldest first",
		"second change to one", "third change to one",
		"every row ends in a verb; re-run this report until it ends `clean`",
		"pfm update pin <local> --keep",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("opening report lacks %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, "docs/refs.md") {
		t.Errorf("opening report flags the URL or the project's own /km:\n%s", report)
	}
	// An indented line whose first field is a status word reads as a row to a
	// report parser (infra/fence/lanes/adopt.sh takes NEW's rows that way).
	statusWords := map[string]bool{
		"UPDATED": true, "NEW": true, "GONE-UPSTREAM": true, "MOVED": true, "LOCAL-DELETED": true, "RETIRED-NAME": true,
	}
	for _, line := range strings.Split(report, "\n") {
		fields := strings.Fields(line)
		if strings.HasPrefix(line, "    ") && len(fields) != 0 && statusWords[fields[0]] {
			t.Errorf("report line %q reads as a %s row", line, fields[0])
		}
	}

	for _, step := range [][]string{
		{"pin", ".claude/updated-one.md"},
		{"pin", ".claude/updated-two.md", "--keep", "older drift: this project formats with its own tool"},
		{"ignore", "project/new.md"},
		{"drop", ".claude/gone.md", ".claude/deleted.md"},
		{"ignore", "project/deleted.md"},
		{"accept", "docs/dev/backlog", "--reason", "records of finished waves; rewriting them falsifies the record"},
	} {
		if code, stdout, stderr := fixture.sessionVerb(step...); code != 0 {
			t.Fatalf("pfm update %v code=%d stdout=%q stderr=%q", step, code, stdout, stderr)
		}
	}
	writeProjectFixtureFile(t, fixture.project, "src/notes.txt", "run /flights:spec now\n")

	code, report = fixture.sessionReport()
	lines := strings.Split(strings.TrimSpace(report), "\n")
	if code != 0 || lines[len(lines)-1] != "clean" {
		t.Fatalf("closing report code=%d, want 0 and a last line `clean`:\n%s", code, report)
	}
	for _, want := range []string{
		"KEPT          1", "older drift: this project formats with its own tool",
		"untaken upstream change: git -C " + fixture.store + " diff " + fixture.oldSHA,
		"ACCEPTED      1", "docs/dev/backlog", "records of finished waves",
		"RETIRED-NAME  0",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("closing report lacks %q:\n%s", want, report)
		}
	}
	baseline, err := professor.Load(fixture.project)
	if err != nil {
		t.Fatal(err)
	}
	kept := baseline.Files[".claude/updated-two.md"].Kept
	if kept == nil || kept.FromSHA != fixture.oldSHA ||
		baseline.Files[".claude/updated-two.md"].PinnedSHA == fixture.oldSHA {
		t.Fatalf(
			"keep did not advance the pin and record where the untaken change starts: %#v",
			baseline.Files[".claude/updated-two.md"],
		)
	}
}

// TestKeepPinRefusalsAndLaterChanges: --keep needs a reason, an UPDATED
// file and a single named file; a kept file that upstream changes again shows
// its earlier decision and keeps the earliest untaken commit; porting the
// file whole clears the decision; accept refuses a path no hit lives at.
func TestKeepPinRefusalsAndLaterChanges(t *testing.T) {
	t.Parallel()
	fixture := newProjectUpdateFixture(t)
	for _, refused := range []struct {
		args []string
		code int
	}{
		{[]string{"pin", ".claude/updated-one.md", "--keep", "  "}, 2},
		{[]string{"pin", ".claude/updated-one.md", "--keep", ""}, 2},
		{[]string{"pin", "--all", "--keep", "why"}, 2},
		{[]string{"pin", ".claude/updated-one.md", ".claude/updated-two.md", "--keep", "why"}, 2},
		{[]string{"pin", ".claude/updated-one.md", "--keep", "why", "--unkeep"}, 2},
		{[]string{"pin", "--all", "--unkeep"}, 2},
		{[]string{"pin", ".claude/current.md", "--keep", "why"}, 1},
		{[]string{"accept", "src", "--reason", "no hit lives here"}, 1},
		{[]string{"accept", "docs"}, 2},
	} {
		if code, stdout, stderr := fixture.sessionVerb(refused.args...); code != refused.code {
			t.Errorf(
				"pfm update %v code=%d, want %d (stdout=%q stderr=%q)",
				refused.args,
				code,
				refused.code,
				stdout,
				stderr,
			)
		}
	}
	before := fixture.baselineText(t)

	if code, stdout, stderr := fixture.sessionVerb(
		"pin",
		".claude/updated-one.md",
		"--keep",
		"first reason",
	); code != 0 {
		t.Fatalf("keep code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	fixture.upstreamCommit(t, "project/updated-one.md", "changed again\n", "later change")
	_, report := fixture.sessionReport()
	if !strings.Contains(report, "kept earlier (") || !strings.Contains(report, "first reason") {
		t.Errorf("a kept file that upstream changed again does not show the earlier decision:\n%s", report)
	}
	if code, stdout, stderr := fixture.sessionVerb(
		"pin",
		".claude/updated-one.md",
		"--keep",
		"second reason",
	); code != 0 {
		t.Fatalf("second keep code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	baseline, err := professor.Load(fixture.project)
	if err != nil {
		t.Fatal(err)
	}
	if kept := baseline.Files[".claude/updated-one.md"].Kept; kept == nil || kept.Reason != "second reason" ||
		kept.FromSHA != fixture.oldSHA {
		t.Fatalf("second keep lost the earliest untaken commit: %#v", kept)
	}

	fixture.upstreamCommit(t, "project/updated-one.md", "changed a third time\n", "last change")
	if code, stdout, stderr := fixture.sessionVerb("pin", ".claude/updated-one.md"); code != 0 {
		t.Fatalf("port pin code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if baseline, err = professor.Load(fixture.project); err != nil ||
		baseline.Files[".claude/updated-one.md"].Kept == nil {
		t.Fatalf(
			"a plain pin dropped the recorded keep, so the untaken change is lost: %#v (err=%v)",
			baseline.Files[".claude/updated-one.md"],
			err,
		)
	}
	if code, _, _ := fixture.sessionVerb("pin", ".claude/updated-two.md", "--unkeep"); code != 1 {
		t.Errorf("--unkeep on a file with no recorded keep code=%d, want 1", code)
	}
	if code, stdout, stderr := fixture.sessionVerb("pin", ".claude/updated-one.md", "--unkeep"); code != 0 {
		t.Fatalf("unkeep code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if baseline, err = professor.Load(fixture.project); err != nil ||
		baseline.Files[".claude/updated-one.md"].Kept != nil {
		t.Fatalf("--unkeep left the keep on record: %#v (err=%v)", baseline.Files[".claude/updated-one.md"], err)
	}
	if before == fixture.baselineText(t) {
		t.Fatal("baseline never changed")
	}
}

func (f projectUpdateFixture) baselineText(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(professor.BaselinePath(f.project))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestUpdatedRowListsTheOldestCommitsFirstPastTheCap: a pin many commits back
// lists the oldest commits after it — never the newest with the oldest dropped —
// and counts the newer ones it leaves out.
func TestUpdatedRowListsTheOldestCommitsFirstPastTheCap(t *testing.T) {
	t.Parallel()
	fixture := newProjectUpdateFixture(t)
	fixture.upstreamCommits(
		t,
		"project/updated-one.md",
		45,
		func(index int) string { return fmt.Sprintf("version %d\n", index) },
		func(index int) string { return fmt.Sprintf("step %02d", index) },
	)
	_, report := fixture.sessionReport()
	// The fixture's own "new templates" commit is the oldest after the pin.
	if !strings.Contains(report, " new templates\n") || !strings.Contains(report, " step 01\n") ||
		!strings.Contains(report, " step 39\n") {
		t.Errorf("the list lacks the oldest commits (new templates, step 01 … step 39):\n%s", report)
	}
	if strings.Contains(report, " step 40\n") || strings.Contains(report, " step 45\n") {
		t.Errorf("the list holds commits past the cap:\n%s", report)
	}
	if !strings.Contains(report, "… and 6 newer commit(s) not listed: git log --reverse ") {
		t.Errorf("the list does not count the commits it leaves out (46 since the pin, 40 listed):\n%s", report)
	}
}

// TestRecordedReasonsRenderOnOneLine: a keep or accept reason taken from
// baseline.json with a newline never forges report lines of its own.
func TestRecordedReasonsRenderOnOneLine(t *testing.T) {
	t.Parallel()
	fixture := newProjectUpdateFixture(t)
	if code, stdout, stderr := fixture.sessionVerb(
		"pin", ".claude/updated-one.md", "--keep", "own tool\nclean",
	); code != 0 {
		t.Fatalf("keep code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	_, report := fixture.sessionReport()
	for _, line := range strings.Split(report, "\n") {
		if line == "clean" {
			t.Fatalf("a recorded reason forged a terminal line:\n%s", report)
		}
	}
	if !strings.Contains(report, "own tool clean") {
		t.Errorf("the reason is not rendered on one line:\n%s", report)
	}
}

// TestStaleAcceptRuleHintUndoesItsOwnRule: a rule recorded with --name on a
// path holding a space, once its hit is fixed, prints an undo line whose
// arguments — quoted, --name kept — remove exactly that rule.
func TestStaleAcceptRuleHintUndoesItsOwnRule(t *testing.T) {
	t.Parallel()
	fixture := newProjectUpdateFixture(t)
	writeProjectFixtureFile(t, fixture.project, "docs/old notes.md", "ran /km once\n")
	if code, stdout, stderr := fixture.sessionVerb(
		"accept", "docs/old notes.md", "--name", "/km", "--reason", "a record",
	); code != 0 {
		t.Fatalf("accept code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	writeProjectFixtureFile(t, fixture.project, "docs/old notes.md", "fixed\n")
	_, report := fixture.sessionReport()
	hint := "matches nothing now: pfm update accept --undo 'docs/old notes.md' --name /km"
	if !strings.Contains(report, hint) {
		t.Fatalf("report lacks the runnable undo hint %q:\n%s", hint, report)
	}
	if code, stdout, stderr := fixture.sessionVerb(
		"accept", "--undo", "docs/old notes.md", "--name", "/km",
	); code != 0 || !strings.Contains(stdout, "removed 1 retired-name rule(s)") {
		t.Fatalf("the hint's undo code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	baseline, err := professor.Load(fixture.project)
	if err != nil || len(baseline.RetiredAccepted) != 0 || baseline.Version != professor.BaselineVersion {
		t.Fatalf("after undo: rules=%+v version=%d err=%v, want none at version 1",
			baseline.RetiredAccepted, baseline.Version, err)
	}
}

// Reviewed scanner limits are decisions, not permanent open rows. A name-only
// acceptance cannot claim the unscanned text, and undo restores the warning.
func TestSessionAcceptsReviewedIncompleteCoverage(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name, content string
		hit           bool
	}{
		{"line limit without hits", strings.Repeat("x", retiredMaxLine+1), false},
		{"byte limit with earlier hit", "/jc\n" + strings.Repeat(strings.Repeat("x", 1023)+"\n", retiredMaxRead/1024), true},
	} {
		t.Run(row.name, func(t *testing.T) {
			fixture := newProjectUpdateFixture(t)
			for _, step := range [][]string{
				{"pin", ".claude/updated-one.md", ".claude/updated-two.md"},
				{"drop", ".claude/gone.md", ".claude/deleted.md"},
				{"ignore", "project/new.md", "project/deleted.md"},
			} {
				if code, stdout, stderr := fixture.sessionVerb(step...); code != 0 {
					t.Fatalf("setup %v code=%d stdout=%q stderr=%q", step, code, stdout, stderr)
				}
			}
			const path = "docs/archive.txt"
			writeProjectFixtureFile(t, fixture.project, path, row.content)
			_, before := fixture.sessionReport()
			if !strings.Contains(before, "INCOMPLETE") {
				t.Fatalf("opening report lacks coverage warning:\n%s", before)
			}
			if code, stdout, stderr := fixture.sessionVerb(
				"accept",
				path,
				"--name",
				"/wave",
				"--reason",
				"reviewed",
			); code != 1 {
				t.Fatalf("named rule accepted unseen content: code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			if code, stdout, stderr := fixture.sessionVerb(
				"accept",
				"docs",
				"--coverage",
				"--reason",
				"reviewed archive coverage",
			); code != 0 {
				t.Fatalf("reviewed coverage accept code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			baseline, err := professor.Load(fixture.project)
			if err != nil || len(baseline.RetiredAccepted) != 1 || baseline.RetiredAccepted[0].Path != "docs" ||
				baseline.RetiredAccepted[0].Reason != "reviewed archive coverage" ||
				!baseline.RetiredAccepted[0].Coverage || baseline.Version != professor.BaselineCoverageVersion {
				t.Fatalf("persistent coverage rule = %+v, err=%v", baseline.RetiredAccepted, err)
			}
			if row.hit {
				_, report := fixture.sessionReport()
				rows, err := professor.OpenProjectRows(fixture.project, fixture.runtime.Paths.Home, ScanRetiredNames)
				if err != nil || len(rows) != 1 || !strings.Contains(report, "RETIRED-NAME  1") {
					t.Fatalf("coverage must leave earlier hit open: rows=%+v err=%v report=%s", rows, err, report)
				}
				if code, stdout, stderr := fixture.sessionVerb(
					"accept", path, "--name", "/jc", "--reason", "historical mention stays",
				); code != 0 {
					t.Fatalf("separate hit accept code=%d stdout=%q stderr=%q", code, stdout, stderr)
				}
			}
			code, report := fixture.sessionReport()
			if code != 0 || !strings.HasSuffix(strings.TrimSpace(report), "\nclean") ||
				!strings.Contains(report, "accepted incomplete coverage: "+path) ||
				strings.Contains(report, "matches nothing now") {
				t.Fatalf("accepted coverage must be explicit and close review: code=%d\n%s", code, report)
			}
			rows, err := professor.OpenProjectRows(fixture.project, fixture.runtime.Paths.Home, ScanRetiredNames)
			if err != nil || len(rows) != 0 {
				t.Fatalf("accepted coverage remains open: rows=%+v err=%v", rows, err)
			}
			failure := func(string, map[string]bool) professor.RetiredNameScan {
				return professor.RetiredNameScan{
					Failures: []professor.RetiredNameFailure{{Path: path, Error: "read failed"}},
				}
			}
			var failed bytes.Buffer
			if code := professor.RunProjectUpdates(
				fixture.project,
				fixture.runtime.Paths.Home,
				false,
				&failed,
				failure,
			); code != 3 ||
				!strings.Contains(failed.String(), "read failed") {
				t.Fatalf("accepted path hid failure: code=%d report=%s", code, failed.String())
			}
			var out, errOut bytes.Buffer
			if code := professor.RunProjectUpdate(
				"accept",
				[]string{"docs", "--coverage", "--reason", "reviewed", "--root", fixture.project},
				&out,
				&errOut,
				fixture.runtime,
				failure,
			); code != 1 {
				t.Fatalf("read failure was accepted: code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
			}
			if code, stdout, stderr := fixture.sessionVerb("accept", "--undo", "--coverage", "docs"); code != 0 {
				t.Fatalf("undo code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			baseline, err = professor.Load(fixture.project)
			wantRules, wantVersion := 0, professor.BaselineVersion
			if row.hit {
				wantRules, wantVersion = 1, professor.BaselineDecisionsVersion
			}
			if err != nil || len(baseline.RetiredAccepted) != wantRules || baseline.Version != wantVersion {
				t.Fatalf("coverage undo removed wrong scope: baseline=%+v err=%v", baseline, err)
			}
			if row.hit && (baseline.RetiredAccepted[0].Coverage || baseline.RetiredAccepted[0].Name != "/jc") {
				t.Fatalf("coverage undo lost hit decision: %+v", baseline.RetiredAccepted)
			}
			_, report = fixture.sessionReport()
			if !strings.Contains(report, "INCOMPLETE") || strings.HasSuffix(strings.TrimSpace(report), "\nclean") {
				t.Fatalf("undo did not reopen coverage:\n%s", report)
			}
		})
	}
}
