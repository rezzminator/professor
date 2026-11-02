package installer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/retired"
)

// stageRetiredLists points home's source-repo marker at a clone holding lists
// as its retired lists, for code that reads them through home.
func stageRetiredLists(t *testing.T, home string, lists retired.Lists) {
	t.Helper()
	raw, err := json.Marshal(lists)
	if err != nil {
		t.Fatal(err)
	}
	clone := t.TempDir()
	writeFixture(t, filepath.Join(clone, filepath.FromSlash(retired.FileRel)), string(raw))
	if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
		t.Fatal(err)
	}
}

func TestRetiredListsReadsTheCloneOnce(t *testing.T) {
	repo := t.TempDir()
	file := filepath.Join(repo, filepath.FromSlash(retired.FileRel))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(`{"units":["old.service"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	installer := &engine{options: Options{Home: t.TempDir(), SourceRepo: repo}}
	want := retired.Lists{Units: []string{"old.service"}}
	got, err := installer.retiredLists()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("retiredLists() = %+v, %v; want %+v from %s", got, err, want, file)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if got, err := installer.retiredLists(); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("second retiredLists() = %+v, %v; want the first read %+v", got, err, want)
	}
}

func TestRetiredListsMissingFileIsAnError(t *testing.T) {
	repo := t.TempDir()
	installer := &engine{options: Options{Home: t.TempDir(), SourceRepo: repo}}
	_, err := installer.retiredLists()
	if err == nil || !strings.Contains(err.Error(), filepath.Join(repo, filepath.FromSlash(retired.FileRel))) {
		t.Fatalf("retiredLists() error = %v; want a read error naming %s", err, retired.FileRel)
	}
	if _, again := installer.retiredLists(); again != err {
		t.Fatalf("second retiredLists() error = %v; want the first %v", again, err)
	}
}
