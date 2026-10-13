package professor

import (
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
