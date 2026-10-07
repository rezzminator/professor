package workbench

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestWorkbenchCacheRoundTrip(t *testing.T) {
	root, scribe := benchFixture(t)
	lab := filepath.Join(root, ".professor", "lab")
	seedBench(t, lab, "{broken")
	benches, _ := Discover([]string{root})
	fault := WalkError{Root: root, Path: filepath.Join(root, "docs", "locked"), Err: errors.New("permission denied")}
	path := filepath.Join(t.TempDir(), "state", "workbenches.json")
	if err := WriteCache(path, benches, []WalkError{fault}); err != nil {
		t.Fatal(err)
	}
	got, faults, err := ReadCache(path)
	if err != nil || len(got) != 2 || len(faults) != 1 {
		t.Fatalf("cache = %#v, %v, %v; want both benches and walk error", got, faults, err)
	}
	if got[0].Dir != lab || got[0].Err == nil || got[1].Dir != scribe || got[1].Key != "acme › Scribe" ||
		faults[0].Error() != fault.Error() {
		t.Fatalf("round trip = %#v, %v", got, faults)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Version     int                                  `json:"version"`
		Workbenches []struct{ Dir, Root string }         `json:"workbenches"`
		Errors      []struct{ Root, Path, Error string } `json:"errors"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Version != 1 || len(wire.Workbenches) != 2 || wire.Errors[0].Error != "permission denied" {
		t.Fatalf("cache wire = %+v", wire)
	}
}

func TestWorkbenchCacheMissingAndCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workbenches.json")
	benches, faults, err := ReadCache(path)
	if benches != nil || faults != nil || err != nil {
		t.Fatalf("missing cache = %v, %v, %v", benches, faults, err)
	}
	for _, raw := range []string{"{broken", `{"version":2}`, `{"version":1} {}`, `{"version":1,"workbenches":[{"dir":"relative","root":"/work/acme"}]}`} {
		writeBenchFile(t, path, raw)
		if _, _, err := ReadCache(path); err == nil {
			t.Fatalf("corrupt cache %q accepted", raw)
		}
	}
}

func TestWorkbenchCacheStaleEntryAndReload(t *testing.T) {
	root, scribe := benchFixture(t)
	path := filepath.Join(t.TempDir(), "workbenches.json")
	if err := WriteCache(path, []Bench{LoadBench(scribe, root)}, nil); err != nil {
		t.Fatal(err)
	}
	before, _, err := ReadCache(path)
	if err != nil || len(before) != 1 {
		t.Fatalf("seed cache = %v, %v", before, err)
	}
	writeBenchFile(t, paths.WorkbenchManifest(scribe), `{"prompt":"scribe.md","title":"Changed"}`)
	got, _, err := ReadCache(path)
	if err != nil || len(got) != 1 || got[0].Key != "acme › Changed" {
		t.Fatalf("reloaded = %v, %v", got, err)
	}
	if err := os.Remove(paths.WorkbenchManifest(scribe)); err != nil {
		t.Fatal(err)
	}
	got, _, err = ReadCache(path)
	if err != nil || len(got) != 0 {
		t.Fatalf("stale cache = %v, %v", got, err)
	}
}

func TestWorkbenchCacheDuplicateTitles(t *testing.T) {
	root, _ := benchFixture(t)
	seedBench(t, filepath.Join(root, "notes", "scribe"), exampleManifest)
	benches, _ := Discover([]string{root})
	path := filepath.Join(t.TempDir(), "workbenches.json")
	if err := WriteCache(path, benches, nil); err != nil {
		t.Fatal(err)
	}
	got, _, err := ReadCache(path)
	if err != nil || !reflect.DeepEqual(got, benches) {
		t.Fatalf("duplicate cache = %#v, %v; want %#v", got, err, benches)
	}
}

func TestWorkbenchCacheWriteFailure(t *testing.T) {
	path := t.TempDir()
	if err := WriteCache(path, nil, nil); err == nil {
		t.Fatal("writing cache over directory succeeded")
	}
}
