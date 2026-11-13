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
		output.String() != "professor: current 1 · review-required 0\n" {
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

func TestWriteProjectHumanRendersEachRowKind(t *testing.T) {
	store := Store{Root: "/work/blueprint", Templates: "/work/blueprint/templates/project", SHA: "def5678"}
	updated := func(pin, diff, diffError string) projectReportItem {
		return projectReportItem{
			Status: projectUpdated, Local: ".claude/settings.json", Template: "settings.json",
			Pin: FilePin{Template: "settings.json", PinnedSHA: pin}, Diff: diff, DiffError: diffError,
		}
	}
	one := func(item projectReportItem) projectReport {
		return projectReport{
			Root: "/work/project", Store: store, Counts: map[projectStatus]int{item.Status: 1},
			Items: []projectReportItem{item},
		}
	}
	withNew := one(updated("abc1234", "", "bad revision"))
	withNew.Counts[projectNew] = 1
	withNew.Items = append(withNew.Items, projectReportItem{Status: projectNew, Template: "project/NEW.md"})
	for _, tc := range []struct {
		name   string
		report projectReport
		want   string
	}{
		{"gone upstream", projectReport{
			Counts: map[projectStatus]int{projectGoneUpstream: 1},
			Items:  []projectReportItem{{Status: projectGoneUpstream, Local: "legacy.md", Template: "project/legacy.md"}},
		}, "professor:   blueprint  → \n  current       0\n  ignored       0\n  UPDATED       0\n  NEW           0\n  GONE-UPSTREAM 1\n    legacy.md   project/legacy.md — retired upstream — delete it and pfm update drop legacy.md; keep it and drop only its pin if the project still uses it\n  MOVED         0\n  LOCAL-DELETED 0\n  every row ends in a verb; re-run this report until it ends `clean` — a row left open is not a decision:\n    · UPDATED         port the intent of each upstream commit, then pfm update pin <local>; keeping your version on purpose: pfm update pin <local> --keep \"<what you did not take, and why>\"\n    · NEW             adopt, then pfm update pin --template <template> <local>; not for this project: pfm update ignore <template>\n    · GONE-UPSTREAM   pfm update drop <local>\n    · LOCAL-DELETED   restore the file, or pfm update drop <local> then pfm update ignore <template> (a dropped pin's template reads NEW); MOVED: the verbs on the row\n    · RETIRED-NAME    edit the line to its successor; a mention that must stay: pfm update accept <path> --reason \"<why>\" [--name <name>]\n    · INCOMPLETE      inspect the unscanned text; accept reviewed coverage: pfm update accept <path> --coverage --reason \"<why>\"; read failures must be fixed\n  run the verbs one at a time: each rewrites .professor/baseline.json\nREVIEW REQUIRED — 1 items; nothing was written.\n"},
		{"self-hosted pin", one(updated(UnknownSelfHostedSHA, "", "")), "professor: /work/project  blueprint  → def5678\n  current       0\n  ignored       0\n  UPDATED       1\n    .claude/settings.json   settings.json  pinned @self-hosted@unknown\n      review: self-hosted pin or store — no git history, so an exact self-hosted@unknown..def5678 diff cannot run; comparing the current template against your local file instead:\n      review: diff /work/project/.claude/settings.json /work/blueprint/templates/project/settings.json\n      port what applies into .claude/settings.json, keep the project's own edits, then: pfm update pin .claude/settings.json\n      or keep your version on purpose, on record: pfm update pin .claude/settings.json --keep \"<what you did not take, and why>\"\n  NEW           0\n  GONE-UPSTREAM 0\n  MOVED         0\n  LOCAL-DELETED 0\n  every row ends in a verb; re-run this report until it ends `clean` — a row left open is not a decision:\n    · UPDATED         port the intent of each upstream commit, then pfm update pin <local>; keeping your version on purpose: pfm update pin <local> --keep \"<what you did not take, and why>\"\n    · NEW             adopt, then pfm update pin --template <template> <local>; not for this project: pfm update ignore <template>\n    · GONE-UPSTREAM   pfm update drop <local>\n    · LOCAL-DELETED   restore the file, or pfm update drop <local> then pfm update ignore <template> (a dropped pin's template reads NEW); MOVED: the verbs on the row\n    · RETIRED-NAME    edit the line to its successor; a mention that must stay: pfm update accept <path> --reason \"<why>\" [--name <name>]\n    · INCOMPLETE      inspect the unscanned text; accept reviewed coverage: pfm update accept <path> --coverage --reason \"<why>\"; read failures must be fixed\n  run the verbs one at a time: each rewrites .professor/baseline.json\nREVIEW REQUIRED — 1 items; nothing was written.\n"},
		{"upstream diff", one(updated("abc1234", "-old line\n+new line\n", "")), "professor: /work/project  blueprint  → def5678\n  current       0\n  ignored       0\n  UPDATED       1\n    .claude/settings.json   settings.json  pinned @abc1234\n      upstream change: git -C /work/blueprint diff abc1234 -- templates/settings.json\n      -old line\n      +new line\n      port what applies into .claude/settings.json, keep the project's own edits, then: pfm update pin .claude/settings.json\n      or keep your version on purpose, on record: pfm update pin .claude/settings.json --keep \"<what you did not take, and why>\"\n  NEW           0\n  GONE-UPSTREAM 0\n  MOVED         0\n  LOCAL-DELETED 0\n  every row ends in a verb; re-run this report until it ends `clean` — a row left open is not a decision:\n    · UPDATED         port the intent of each upstream commit, then pfm update pin <local>; keeping your version on purpose: pfm update pin <local> --keep \"<what you did not take, and why>\"\n    · NEW             adopt, then pfm update pin --template <template> <local>; not for this project: pfm update ignore <template>\n    · GONE-UPSTREAM   pfm update drop <local>\n    · LOCAL-DELETED   restore the file, or pfm update drop <local> then pfm update ignore <template> (a dropped pin's template reads NEW); MOVED: the verbs on the row\n    · RETIRED-NAME    edit the line to its successor; a mention that must stay: pfm update accept <path> --reason \"<why>\" [--name <name>]\n    · INCOMPLETE      inspect the unscanned text; accept reviewed coverage: pfm update accept <path> --coverage --reason \"<why>\"; read failures must be fixed\n  run the verbs one at a time: each rewrites .professor/baseline.json\nREVIEW REQUIRED — 1 items; nothing was written.\n"},
		{"unreadable diff", one(updated("abc1234", "", "git diff: git exited with status 128: fatal: bad revision 'abc1234'")), "professor: /work/project  blueprint  → def5678\n  current       0\n  ignored       0\n  UPDATED       1\n    .claude/settings.json   settings.json  pinned @abc1234\n      upstream change UNREADABLE — git diff: git exited with status 128: fatal: bad revision 'abc1234'\n      port what applies into .claude/settings.json, keep the project's own edits, then: pfm update pin .claude/settings.json\n      or keep your version on purpose, on record: pfm update pin .claude/settings.json --keep \"<what you did not take, and why>\"\n  NEW           0\n  GONE-UPSTREAM 0\n  MOVED         0\n  LOCAL-DELETED 0\n  every row ends in a verb; re-run this report until it ends `clean` — a row left open is not a decision:\n    · UPDATED         port the intent of each upstream commit, then pfm update pin <local>; keeping your version on purpose: pfm update pin <local> --keep \"<what you did not take, and why>\"\n    · NEW             adopt, then pfm update pin --template <template> <local>; not for this project: pfm update ignore <template>\n    · GONE-UPSTREAM   pfm update drop <local>\n    · LOCAL-DELETED   restore the file, or pfm update drop <local> then pfm update ignore <template> (a dropped pin's template reads NEW); MOVED: the verbs on the row\n    · RETIRED-NAME    edit the line to its successor; a mention that must stay: pfm update accept <path> --reason \"<why>\" [--name <name>]\n    · INCOMPLETE      inspect the unscanned text; accept reviewed coverage: pfm update accept <path> --coverage --reason \"<why>\"; read failures must be fixed\n  run the verbs one at a time: each rewrites .professor/baseline.json\nFAILED — 1 item(s) could not be read; nothing was written.\n"},
		{"unreadable diff beside a NEW row", withNew, "professor: /work/project  blueprint  → def5678\n  current       0\n  ignored       0\n  UPDATED       1\n    .claude/settings.json   settings.json  pinned @abc1234\n      upstream change UNREADABLE — bad revision\n      port what applies into .claude/settings.json, keep the project's own edits, then: pfm update pin .claude/settings.json\n      or keep your version on purpose, on record: pfm update pin .claude/settings.json --keep \"<what you did not take, and why>\"\n  NEW           1\n    project/NEW.md — adopt: copy/adapt it locally, then pfm update pin --template project/NEW.md <local> — or ignore\n  GONE-UPSTREAM 0\n  MOVED         0\n  LOCAL-DELETED 0\n  every row ends in a verb; re-run this report until it ends `clean` — a row left open is not a decision:\n    · UPDATED         port the intent of each upstream commit, then pfm update pin <local>; keeping your version on purpose: pfm update pin <local> --keep \"<what you did not take, and why>\"\n    · NEW             adopt, then pfm update pin --template <template> <local>; not for this project: pfm update ignore <template>\n    · GONE-UPSTREAM   pfm update drop <local>\n    · LOCAL-DELETED   restore the file, or pfm update drop <local> then pfm update ignore <template> (a dropped pin's template reads NEW); MOVED: the verbs on the row\n    · RETIRED-NAME    edit the line to its successor; a mention that must stay: pfm update accept <path> --reason \"<why>\" [--name <name>]\n    · INCOMPLETE      inspect the unscanned text; accept reviewed coverage: pfm update accept <path> --coverage --reason \"<why>\"; read failures must be fixed\n  run the verbs one at a time: each rewrites .professor/baseline.json\nFAILED — 1 item(s) could not be read; nothing was written.\n"},
		{"empty diff", one(updated("abc1234", "", "")), "professor: /work/project  blueprint  → def5678\n  current       0\n  ignored       0\n  UPDATED       1\n    .claude/settings.json   settings.json  pinned @abc1234\n      upstream change EMPTY — the pin was taken from an uncommitted or untracked store file, so git cannot show the change; compare by hand: diff /work/project/.claude/settings.json /work/blueprint/templates/project/settings.json\n      port what applies into .claude/settings.json, keep the project's own edits, then: pfm update pin .claude/settings.json\n      or keep your version on purpose, on record: pfm update pin .claude/settings.json --keep \"<what you did not take, and why>\"\n  NEW           0\n  GONE-UPSTREAM 0\n  MOVED         0\n  LOCAL-DELETED 0\n  every row ends in a verb; re-run this report until it ends `clean` — a row left open is not a decision:\n    · UPDATED         port the intent of each upstream commit, then pfm update pin <local>; keeping your version on purpose: pfm update pin <local> --keep \"<what you did not take, and why>\"\n    · NEW             adopt, then pfm update pin --template <template> <local>; not for this project: pfm update ignore <template>\n    · GONE-UPSTREAM   pfm update drop <local>\n    · LOCAL-DELETED   restore the file, or pfm update drop <local> then pfm update ignore <template> (a dropped pin's template reads NEW); MOVED: the verbs on the row\n    · RETIRED-NAME    edit the line to its successor; a mention that must stay: pfm update accept <path> --reason \"<why>\" [--name <name>]\n    · INCOMPLETE      inspect the unscanned text; accept reviewed coverage: pfm update accept <path> --coverage --reason \"<why>\"; read failures must be fixed\n  run the verbs one at a time: each rewrites .professor/baseline.json\nREVIEW REQUIRED — 1 items; nothing was written.\n"},
	} {
		var output bytes.Buffer
		writeProjectHuman(&output, tc.report)
		if got := output.String(); got != tc.want {
			t.Errorf("%s: report = %q, want %q", tc.name, got, tc.want)
		}
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

	// Upstream (still self-hosted, still no .git) moves .claude/settings.json forward —
	// the project's pin goes stale, classifying projectUpdated.
	template := filepath.Join(source, "templates", "project", "settings.json")
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
	wantLocal := filepath.Join(project, ".claude", "settings.json")
	wantTemplate := filepath.Join(source, "templates", "project", "settings.json")
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
	if got := human.String(); got != "NOT-MANAGED — no .professor/baseline.json at or above this directory (expected in the Professor source clone); run `pfm doctor --project-updates` inside each adopted project\n" {
		t.Fatalf("WriteProjectUnmanaged(human) = %q", got)
	}

	var jsonBuffer bytes.Buffer
	writeProjectUnmanaged(&jsonBuffer, true)
	var object map[string]any
	if err := json.Unmarshal(jsonBuffer.Bytes(), &object); err != nil {
		t.Fatalf("JSON output is not one object: %v", err)
	}
	terminal, ok := object["terminal"].(string)
	if !ok ||
		terminal != "NOT-MANAGED — no .professor/baseline.json at or above this directory (expected in the Professor source clone); run `pfm doctor --project-updates` inside each adopted project" {
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
	code := RunProjectUpdate("", []string{"--root", root}, &stdout, &stderr, config.Runtime{}, nil)
	if got, want := strings.ReplaceAll(
		stdout.String(),
		root,
		"{root}",
	), "FAILED — UNREADABLE {root}/.professor/baseline.json: stat {root}/.professor/baseline.json: too many levels of symbolic links\n"; code != 3 ||
		got != want {
		t.Fatalf("RunProjectUpdate() code %d stdout = %q, want %q (stderr %q)", code, got, want, stderr.String())
	}
}
