package sqlitedb

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"hostops/pfm/internal/obs"
)

// TestOpenersRecordUnderTheDBComponent: the three openers are the db door's
// open records — kind, path, dur_ms — and a foreign open of a file that is
// not a database still records (the open itself succeeds lazily), while a
// store whose directory cannot be created is an ERROR record.
func TestOpenersRecordUnderTheDBComponent(t *testing.T) {
	ctx, recorder := obs.Test(t)
	root := t.TempDir()
	store, err := OpenStore(ctx, filepath.Join(root, "own", "pfm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	readOnly, err := OpenReadOnly(filepath.Join(root, "own", "pfm.db"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = readOnly.Close() })
	readWrite, err := OpenReadWrite(filepath.Join(root, "own", "pfm.db"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = readWrite.Close() })
	blocked := filepath.Join(root, "file")
	if err := writeFile(blocked); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(ctx, filepath.Join(blocked, "under-a-file", "pfm.db")); err == nil {
		t.Fatal("a store under a regular file opened")
	}
	records := recorder.Records()
	if len(records) != 4 {
		t.Fatalf("records = %d, want one per open: %s", len(records), recorder.Raw())
	}
	for index, kind := range []string{"store", "readonly", "readwrite", "store"} {
		record := records[index]
		if record.Message != "db.open" {
			t.Fatalf("record %d = %s, want db.open", index, record.Message)
		}
		if comp, _ := record.Field(obs.FieldComp); comp != "db" {
			t.Fatalf("comp = %v, want db", comp)
		}
		if got, _ := record.Field("kind"); got != kind {
			t.Fatalf("record %d kind = %v, want %s", index, got, kind)
		}
		if path, _ := record.Field("path"); path == nil || path == "" {
			t.Fatalf("record %d has no path: %v", index, record.Fields)
		}
		if _, found := record.Field(obs.FieldDur); !found {
			t.Fatalf("record %d has no dur_ms", index)
		}
	}
	if records[3].Level != "ERROR" {
		t.Fatalf("the failed open logged at %s, want ERROR", records[3].Level)
	}
}

func writeFile(path string) error { return os.WriteFile(path, []byte("not a directory"), 0o600) }
