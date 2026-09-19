package professor

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// TestProfessorDoctorReviewRequiredMovesWarningTally pins L3-F15: `pfm doctor`
// must not stay clean while a managed project has review-required drift.
// `pfm update check` already returns exit 3 in this exact state
// (renderProjectCheck); PrintDoctor's return value feeds straight into
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
	// is 1, exactly the state `pfm update check` reports with exit 3.
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
			"PrintDoctor() warnings=%d with review-required drift outstanding — doctor stays clean while `pfm update check` exits 3 in the same state",
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

func TestWriteProjectUnmanagedHumanAndJSON(t *testing.T) {
	var human bytes.Buffer
	writeProjectUnmanaged(&human, false)
	if got := human.String(); !strings.HasPrefix(got, "NOT-MANAGED — ") ||
		!strings.Contains(got, "pfm update check") || strings.Contains(got, "pfm init") {
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
