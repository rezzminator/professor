package sqlitedb

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func pragma(t *testing.T, database *sql.DB, name string) string {
	t.Helper()
	var value string
	if err := database.QueryRowContext(context.Background(), "PRAGMA "+name).Scan(&value); err != nil {
		t.Fatalf("PRAGMA %s: %v", name, err)
	}
	return value
}

// TestOpenStoreAppliesTheStorePragmaSet pins the one pragma set every pfm
// store runs on: its directory created, WAL, synchronous=NORMAL (1), foreign
// keys on, and the store busy timeout.
func TestOpenStoreAppliesTheStorePragmaSet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "store.db")
	database, err := OpenStore(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()
	for name, want := range map[string]string{
		"journal_mode": "wal", "synchronous": "1", "foreign_keys": "1", "busy_timeout": "10000",
	} {
		if got := pragma(t, database, name); got != want {
			t.Errorf("PRAGMA %s = %q, want %q", name, got, want)
		}
	}
}

// TestForeignOpenersKeepTheOwnersSettings pins the other program's store:
// read-only refuses a write, read-write can write, both carry the caller's
// busy timeout, and neither switches the owner's rollback journal to WAL.
func TestForeignOpenersKeepTheOwnersSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owner.db")
	owner, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec("CREATE TABLE threads (id TEXT)"); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	readOnly, err := OpenReadOnly(path, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := readOnly.Close(); err != nil {
			t.Errorf("close readOnly: %v", err)
		}
	}()
	if _, err := readOnly.Exec("INSERT INTO threads VALUES ('a')"); err == nil {
		t.Fatal("a read-only handle accepted a write")
	}
	if got := pragma(t, readOnly, "busy_timeout"); got != "2000" {
		t.Fatalf("read-only busy_timeout = %q, want 2000", got)
	}
	readWrite, err := OpenReadWrite(path, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := readWrite.Close(); err != nil {
			t.Errorf("close readWrite: %v", err)
		}
	}()
	if _, err := readWrite.Exec("INSERT INTO threads VALUES ('a')"); err != nil {
		t.Fatalf("read-write insert: %v", err)
	}
	if got := pragma(t, readWrite, "busy_timeout"); got != "5000" {
		t.Fatalf("read-write busy_timeout = %q, want 5000", got)
	}
	if got := pragma(t, readWrite, "journal_mode"); got != "delete" {
		t.Fatalf("owner's journal_mode = %q, want its own rollback journal kept", got)
	}
}

// TestOpenersReachAPathWithURICharacters pins that a path is a path, not URI
// text: a home or PFM_FLEET_DB directory holding "?", "#" or "%41" must open
// that very file — never a truncated or percent-decoded neighbor, which would
// read as an empty database.
func TestOpenersReachAPathWithURICharacters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "odd?dir#%41", "store.db")
	store, err := OpenStore(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Exec("CREATE TABLE marker (id TEXT)"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("OpenStore did not create the named file: %v", err)
	}
	for name, open := range map[string]func(string, time.Duration) (*sql.DB, error){
		"read-only": OpenReadOnly, "read-write": OpenReadWrite,
	} {
		database, err := open(path, time.Second)
		if err != nil {
			t.Fatalf("%s open: %v", name, err)
		}
		var count int
		if err := database.QueryRow("SELECT count(*) FROM marker").Scan(&count); err != nil {
			t.Fatalf("%s handle does not see the store's table: %v", name, err)
		}
		_ = database.Close()
	}
}
