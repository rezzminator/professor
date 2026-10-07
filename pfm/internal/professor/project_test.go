package professor

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
)

func TestProfessorDoctorProjectLine(t *testing.T) {
	store := t.TempDir()
	if err := os.MkdirAll(filepath.Join(store, "templates", "project"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "VERSION"), []byte("0.1.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	template := filepath.Join(store, "templates", "project", "current.md")
	if err := os.WriteFile(template, []byte("current\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".professor"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"interview":{"blueprint_clone_path":"` + store + `"}}`)
	if err := os.WriteFile(filepath.Join(project, ".professor", "manifest.json"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "current.md"), []byte("local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hash, err := HashTemplate(template)
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(project, Baseline{
		Version: BaselineVersion,
		Files: map[string]FilePin{
			"current.md": {Template: "project/current.md", TemplateHash: hash},
		},
	}); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if warnings := PrintDoctor(&output, project, t.TempDir()); warnings != 0 ||
		!strings.Contains(output.String(), "professor: current 1 · review-required 0") {
		t.Fatalf("PrintDoctor() warnings=%d output=%q", warnings, output.String())
	}
	if err := os.WriteFile(BaselinePath(project), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if warnings := PrintDoctor(&output, project, t.TempDir()); warnings != 1 ||
		!strings.Contains(output.String(), "professor: UNREADABLE") {
		t.Fatalf("PrintDoctor() unreadable warnings=%d output=%q", warnings, output.String())
	}
}

func TestGoneUpstreamReportRecommendsDeletingRetiredFile(t *testing.T) {
	var output bytes.Buffer
	writeProjectHuman(&output, projectReport{
		Counts: map[projectStatus]int{projectGoneUpstream: 1},
		Items: []projectReportItem{{
			Status:   projectGoneUpstream,
			Local:    "legacy.md",
			Template: "project/legacy.md",
		}},
	})
	want := "retired upstream — delete it and pfm update drop legacy.md; keep it and drop only its pin if the project still uses it"
	if !strings.Contains(output.String(), want) {
		t.Fatalf("GONE-UPSTREAM advice = %q, want %q", output.String(), want)
	}
}

// TestProfessorDoctorReviewRequiredMovesWarningTally pins L3-F15: `pfm doctor`
// must not stay clean while a managed project has review-required drift.
// `pfm doctor --project-updates` already returns exit 1 in this exact state
// (RunProjectUpdates); PrintDoctor's return value feeds straight into
// doctor's own warning tally (doctor.go's `tally.warnings +=
// professor.PrintDoctor(...)`), so PrintDoctor returning 0 here is the bug —
// not a missing wire-up downstream.
func TestProfessorDoctorReviewRequiredMovesWarningTally(t *testing.T) {
	store := t.TempDir()
	if err := os.MkdirAll(filepath.Join(store, "templates", "project"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "VERSION"), []byte("0.1.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	template := filepath.Join(store, "templates", "project", "current.md")
	if err := os.WriteFile(template, []byte("current\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".professor"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"interview":{"blueprint_clone_path":"` + store + `"}}`)
	if err := os.WriteFile(filepath.Join(project, ".professor", "manifest.json"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "current.md"), []byte("local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hash, err := HashTemplate(template)
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(project, Baseline{
		Version: BaselineVersion,
		Files: map[string]FilePin{
			"current.md": {Template: "project/current.md", TemplateHash: hash},
		},
	}); err != nil {
		t.Fatal(err)
	}

	// Upstream moves the template forward — the project's pin is now stale,
	// so buildProjectReport classifies it projectUpdated and reviewRequired()
	// is 1, exactly the state `pfm doctor --project-updates` reports with exit 1.
	if err := os.WriteFile(template, []byte("current v2\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	warnings := PrintDoctor(&output, project, t.TempDir())
	if !strings.Contains(output.String(), "professor: current 0 · review-required 1") {
		t.Fatalf("PrintDoctor() output=%q, want review-required 1", output.String())
	}
	if warnings == 0 {
		t.Fatalf(
			"PrintDoctor() warnings=%d with review-required drift outstanding — doctor stays clean while `pfm doctor --project-updates` exits 1 in the same state",
			warnings,
		)
	}
}

// TestSelfHostedReviewLinePrintsARunnableDiffCommand pins L3-F17: a
// self-hosted (no-.git) blueprint clone always reports its SHA as
// UnknownSelfHostedSHA, both when the file was pinned and now — so the
// "exact git diff" line collapsed to `git diff self-hosted@unknown..
// self-hosted@unknown`, two refs that cannot resolve. In that mode the
// review line must say plainly that no diff is derivable and print a
// command that DOES run: a plain diff of the current template against the
// local file.
func TestSelfHostedReviewLinePrintsARunnableDiffCommand(t *testing.T) {
	source := newScaffoldFixtureStore(t)
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".professor"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"interview":{"blueprint_clone_path":"` + source + `"}}`)
	if err := os.WriteFile(filepath.Join(project, ".professor", "manifest.json"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	var scaffoldOut bytes.Buffer
	if _, err := Scaffold(source, project, false, &scaffoldOut); err != nil {
		t.Fatalf("Scaffold() err = %v, stdout=%q", err, scaffoldOut.String())
	}

	// Upstream (still self-hosted, still no .git) moves CLAUDE.md forward —
	// the project's pin goes stale, classifying projectUpdated.
	template := filepath.Join(source, "templates", "project", "CLAUDE.md")
	if err := os.WriteFile(template, []byte("# fixture contract v2\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	report, err := buildProjectReport(project, t.TempDir())
	if err != nil {
		t.Fatalf("buildProjectReport() err = %v", err)
	}
	if report.Store.SHA != UnknownSelfHostedSHA {
		t.Fatalf("report.Store.SHA = %q, want %q (self-hosted, no .git)", report.Store.SHA, UnknownSelfHostedSHA)
	}
	if report.Counts[projectUpdated] == 0 {
		t.Fatalf("report has no projectUpdated items: %#v", report.Counts)
	}

	var output bytes.Buffer
	writeProjectHuman(&output, report)
	text := output.String()

	unrunnable := "diff " + UnknownSelfHostedSHA + ".." + UnknownSelfHostedSHA
	if strings.Contains(text, unrunnable) {
		t.Fatalf("review line still prints the unrunnable self-hosted@unknown..self-hosted@unknown diff:\n%s", text)
	}
	wantLocal := filepath.Join(project, "CLAUDE.md")
	wantTemplate := filepath.Join(source, "templates", "project", "CLAUDE.md")
	wantRunnable := "diff " + wantLocal + " " + wantTemplate
	if !strings.Contains(text, wantRunnable) {
		t.Fatalf("review line missing the runnable plain diff %q:\n%s", wantRunnable, text)
	}
	if !strings.Contains(text, "self-hosted") {
		t.Fatalf("review line does not say plainly that this is the self-hosted, no-diff-derivable case:\n%s", text)
	}
}

// TestSelfHostedPinReviewLineAgainstAGitStorePrintsTheLocalFileDiff: a file
// pinned while the store was self-hosted carries PinnedSHA
// self-hosted@unknown; once the store is a git clone the exact-diff line
// would be `git diff self-hosted@unknown..<sha>`, a range git cannot resolve.
func TestSelfHostedPinReviewLineAgainstAGitStorePrintsTheLocalFileDiff(t *testing.T) {
	report := projectReport{
		Root:   "/work/project",
		Store:  Store{Root: "/work/blueprint", Templates: "/work/blueprint/templates/project", SHA: "abc1234"},
		Counts: map[projectStatus]int{projectUpdated: 1},
		Items: []projectReportItem{{
			Status:   projectUpdated,
			Local:    "CLAUDE.md",
			Template: "CLAUDE.md",
			Pin:      FilePin{Template: "CLAUDE.md", PinnedSHA: UnknownSelfHostedSHA},
		}},
	}
	var output bytes.Buffer
	writeProjectHuman(&output, report)
	text := output.String()
	if strings.Contains(text, "diff "+UnknownSelfHostedSHA+"..") {
		t.Fatalf("review line prints an unresolvable self-hosted@unknown range:\n%s", text)
	}
	want := "review: diff /work/project/CLAUDE.md /work/blueprint/templates/project/CLAUDE.md"
	if !strings.Contains(text, want) {
		t.Fatalf("review line missing the runnable local-file diff %q:\n%s", want, text)
	}
}

// TestUpdatedRowPrintsUpstreamDiffHeadingIndentedLinesAndGuidance pins the
// 0-contracts § A `UPDATED` row body: the git heading, every diff line
// indented six spaces, then the port-and-pin guidance line.
func TestUpdatedRowPrintsUpstreamDiffHeadingIndentedLinesAndGuidance(t *testing.T) {
	report := projectReport{
		Root:   "/work/project",
		Store:  Store{Root: "/work/blueprint", Templates: "/work/blueprint/templates/project", SHA: "def5678"},
		Counts: map[projectStatus]int{projectUpdated: 1},
		Items: []projectReportItem{{
			Status:   projectUpdated,
			Local:    "CLAUDE.md",
			Template: "CLAUDE.md",
			Pin:      FilePin{Template: "CLAUDE.md", PinnedSHA: "abc1234"},
			Diff:     "-old line\n+new line\n",
		}},
	}
	var output bytes.Buffer
	writeProjectHuman(&output, report)
	text := output.String()
	for _, want := range []string{
		"upstream change: git -C /work/blueprint diff abc1234 -- templates/CLAUDE.md",
		"      -old line",
		"      +new line",
		"port what applies into CLAUDE.md, keep the project's own edits, then: pfm update pin CLAUDE.md",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("UPDATED row missing %q:\n%s", want, text)
		}
	}
}

// TestUpdatedRowPrintsUnreadableAndEmptyDiffVariants pins the two diff
// failure/empty variants: an unreadable diff replaces the heading and body
// with one UNREADABLE line, and an empty diff replaces it with one EMPTY
// line naming a runnable hand comparison — both keep the guidance line.
func TestUpdatedRowPrintsUnreadableAndEmptyDiffVariants(t *testing.T) {
	base := func(item projectReportItem) projectReport {
		return projectReport{
			Root:   "/work/project",
			Store:  Store{Root: "/work/blueprint", Templates: "/work/blueprint/templates/project", SHA: "def5678"},
			Counts: map[projectStatus]int{projectUpdated: 1},
			Items:  []projectReportItem{item},
		}
	}

	var unreadable bytes.Buffer
	writeProjectHuman(&unreadable, base(projectReportItem{
		Status:    projectUpdated,
		Local:     "CLAUDE.md",
		Template:  "CLAUDE.md",
		Pin:       FilePin{Template: "CLAUDE.md", PinnedSHA: "abc1234"},
		DiffError: "git diff: git exited with status 128: fatal: bad revision 'abc1234'",
	}))
	unreadableText := unreadable.String()
	if !strings.Contains(
		unreadableText,
		"upstream change UNREADABLE — git diff: git exited with status 128: fatal: bad revision 'abc1234'",
	) {
		t.Fatalf("UPDATED row missing the UNREADABLE line:\n%s", unreadableText)
	}
	if !strings.Contains(
		unreadableText,
		"port what applies into CLAUDE.md, keep the project's own edits, then: pfm update pin CLAUDE.md",
	) {
		t.Fatalf("UNREADABLE row missing the guidance line:\n%s", unreadableText)
	}
	withNew := base(projectReportItem{
		Status: projectUpdated, Local: "CLAUDE.md", Template: "CLAUDE.md",
		Pin: FilePin{Template: "CLAUDE.md", PinnedSHA: "abc1234"}, DiffError: "bad revision",
	})
	withNew.Counts[projectNew] = 1
	withNew.Items = append(withNew.Items, projectReportItem{Status: projectNew, Template: "project/NEW.md"})
	var precedence bytes.Buffer
	writeProjectHuman(&precedence, withNew)
	wantFailed := "FAILED — 1 item(s) could not be read; nothing was written."
	if got := strings.TrimSpace(precedence.String()); !strings.HasSuffix(got, wantFailed) {
		t.Fatalf("unreadable diff plus NEW terminal = %q", got)
	}

	var empty bytes.Buffer
	writeProjectHuman(&empty, base(projectReportItem{
		Status:   projectUpdated,
		Local:    "CLAUDE.md",
		Template: "CLAUDE.md",
		Pin:      FilePin{Template: "CLAUDE.md", PinnedSHA: "abc1234"},
		Diff:     "",
	}))
	emptyText := empty.String()
	want := "upstream change EMPTY — the pin was taken from an uncommitted or untracked store file, " +
		"so git cannot show the change; compare by hand: diff " +
		"/work/project/CLAUDE.md /work/blueprint/templates/project/CLAUDE.md"
	if !strings.Contains(emptyText, want) {
		t.Fatalf("UPDATED row missing the EMPTY line %q:\n%s", want, emptyText)
	}
	if !strings.Contains(
		emptyText,
		"port what applies into CLAUDE.md, keep the project's own edits, then: pfm update pin CLAUDE.md",
	) {
		t.Fatalf("EMPTY row missing the guidance line:\n%s", emptyText)
	}
	if got := strings.TrimSpace(emptyText); !strings.HasSuffix(got, "REVIEW REQUIRED — 1 items; nothing was written.") {
		t.Fatalf("EMPTY diff terminal = %q", got)
	}
}

func TestWriteProjectUnmanagedHumanAndJSON(t *testing.T) {
	var human bytes.Buffer
	writeProjectUnmanaged(&human, false)
	if got := human.String(); !strings.HasPrefix(got, "NOT-MANAGED — ") ||
		!strings.Contains(got, "pfm doctor --project-updates") || strings.Contains(got, "pfm init") {
		t.Fatalf("WriteProjectUnmanaged(human) = %q", got)
	}

	var jsonBuffer bytes.Buffer
	writeProjectUnmanaged(&jsonBuffer, true)
	var object map[string]any
	if err := json.Unmarshal(jsonBuffer.Bytes(), &object); err != nil {
		t.Fatalf("JSON output is not one object: %v", err)
	}
	terminal, ok := object["terminal"].(string)
	if !ok || !strings.HasPrefix(terminal, "NOT-MANAGED — ") {
		t.Fatalf("JSON terminal=%#v", object["terminal"])
	}
}

func TestRunPostUpdateUnreadableRoot(t *testing.T) {
	root := t.TempDir()
	baseline := BaselinePath(root)
	if err := os.MkdirAll(filepath.Dir(baseline), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(baseline, baseline); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := RunProjectUpdate("", []string{"--root", root}, &stdout, &stderr, config.Runtime{})
	if code != 3 || !strings.HasPrefix(stdout.String(), "FAILED — UNREADABLE ") {
		t.Fatalf(
			"RunProjectUpdate() code=%d stdout=%q stderr=%q, want 3 and FAILED — UNREADABLE",
			code,
			stdout.String(),
			stderr.String(),
		)
	}
}
