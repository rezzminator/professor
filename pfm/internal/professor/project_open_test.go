package professor

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// openRowsFixture is a scaffolded adopter whose blueprint store has since
// changed one pinned template (UPDATED) and gained another (NEW).
func openRowsFixture(t *testing.T) (project string) {
	t.Helper()
	store := newScaffoldFixtureStore(t)
	project = t.TempDir()
	if _, err := Scaffold(store, project, false, io.Discard); err != nil {
		t.Fatalf("Scaffold() err = %v", err)
	}
	manifest, err := json.Marshal(map[string]any{"interview": map[string]string{"blueprint_clone_path": store}})
	if err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		filepath.Join(project, ".professor", "manifest.json"):                string(manifest),
		filepath.Join(store, "templates", "project", "settings.json"):        "{\"changed\": true}\n",
		filepath.Join(store, "templates", "project", "commands", "added.md"): "# added\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return project
}

// TestOpenProjectRowsNamesEveryRowNoVerbClosed: UPDATED by its local file, NEW
// by its template, retired-name hits one row per file with their count, an
// unreadable scan file as UNREADABLE, a file scanned only in part as
// INCOMPLETE (its report never ends `clean`); a project whose baseline cannot be read
// is an error, never an empty list.
func TestOpenProjectRowsNamesEveryRowNoVerbClosed(t *testing.T) {
	project := openRowsFixture(t)
	scan := func(string, map[string]bool) RetiredNameScan {
		return RetiredNameScan{
			Hits: []RetiredNameHit{
				{Path: "scripts/a.sh", Line: 1, Name: "/wave"},
				{Path: "scripts/a.sh", Line: 4, Name: "/wave"},
				{Path: "docs/b.md", Line: 2, Name: "/km"},
			},
			Failures:   []RetiredNameFailure{{Path: "bin/blob", Error: "unreadable"}},
			Incomplete: []RetiredNameFailure{{Path: "data/huge.txt", Error: "scanned the first 1 MiB"}},
		}
	}
	rows, err := OpenProjectRows(project, t.TempDir(), scan)
	if err != nil {
		t.Fatalf("OpenProjectRows() err = %v", err)
	}
	want := []OpenRow{
		{Status: "UPDATED", Name: ".claude/settings.json"},
		{Status: "NEW", Name: "project/commands/added.md"},
		{Status: "RETIRED-NAME", Name: "docs/b.md×1"},
		{Status: "RETIRED-NAME", Name: "scripts/a.sh×2"},
		{Status: "UNREADABLE", Name: "bin/blob"},
		{Status: "INCOMPLETE", Name: "data/huge.txt"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("OpenProjectRows() = %+v, want %+v", rows, want)
	}

	// A pinned template and a NEW template the store cannot read are one
	// UNREADABLE row each; every other row still shows and the report fails.
	raw, err := os.ReadFile(filepath.Join(project, ".professor", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest projectManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	store := manifest.Interview.BlueprintClonePath
	pinnedTemplate := filepath.Join(store, "templates", "project", "rumdl-policy.toml")
	newTemplate := filepath.Join(store, "templates", "project", "commands", "locked.md")
	if err := os.WriteFile(newTemplate, []byte("# locked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{pinnedTemplate, newTemplate} {
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
	}
	rows, err = OpenProjectRows(project, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("OpenProjectRows() with unreadable templates err = %v, want the rows", err)
	}
	wantUnreadable := []OpenRow{
		{Status: "UPDATED", Name: ".claude/settings.json"},
		{Status: "NEW", Name: "project/commands/added.md"},
		{Status: "UNREADABLE", Name: "project/commands/locked.md"},
		{Status: "UNREADABLE", Name: ".rumdl.toml"},
	}
	if !reflect.DeepEqual(rows, wantUnreadable) {
		t.Fatalf("OpenProjectRows() with unreadable templates = %+v, want %+v", rows, wantUnreadable)
	}
	var report bytes.Buffer
	if code := RunProjectUpdates(project, t.TempDir(), false, &report, nil); code != 3 {
		t.Fatalf("RunProjectUpdates() code=%d, want 3:\n%s", code, report.String())
	}
	for _, line := range []string{
		"  UNREADABLE    2\n",
		"    project/commands/locked.md — UNREADABLE " + newTemplate + ": read " + newTemplate + ": permission denied\n",
		"    .rumdl.toml — UNREADABLE " + pinnedTemplate + ": read " + pinnedTemplate + ": permission denied\n",
		"FAILED — 2 item(s) could not be read; nothing was written.\n",
	} {
		if !strings.Contains(report.String(), line) {
			t.Fatalf("report lacks %q:\n%s", line, report.String())
		}
	}
	var doctor bytes.Buffer
	if warnings := PrintDoctor(&doctor, project, t.TempDir()); warnings != 4 ||
		doctor.String() != "professor: current 0 · review-required 2 · UNREADABLE 2\n" {
		t.Fatalf("PrintDoctor() warnings=%d output=%q, want 4 and the UNREADABLE count", warnings, doctor.String())
	}

	if err := os.WriteFile(
		filepath.Join(project, ".professor", "baseline.json"),
		[]byte("{not json"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if rows, err := OpenProjectRows(project, t.TempDir(), scan); err == nil || rows != nil ||
		!strings.Contains(err.Error(), "BASELINE-MALFORMED") {
		t.Fatalf("malformed baseline: rows=%+v err=%v, want a BASELINE-MALFORMED error and no rows", rows, err)
	}
}
