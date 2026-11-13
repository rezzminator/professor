//go:build jail

package sqlitedb_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
	"github.com/rezzminator/professor/pfm/internal/testjail"
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
	dir := t.TempDir()
	testjail.DatabaseFiles(t, dir)
	path := filepath.Join(dir, "nested", "store.db")
	database, err := sqlitedb.OpenStore(context.Background(), path)
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

// TestOpenStoreMemoryKeepsItsDatabaseInMemory pins OpenStore's in-memory double:
// the store busy timeout and foreign keys on a memory journal, one connection,
// a name another handle reaches the same database by, a new database per call,
// InMemory true for the names it created alone, and no file written.
func TestOpenStoreMemoryKeepsItsDatabaseInMemory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	ctx := context.Background()
	closeAll := func(databases ...*sql.DB) {
		for _, database := range databases {
			if err := database.Close(); err != nil {
				t.Errorf("close database: %v", err)
			}
		}
	}
	database, name, err := sqlitedb.OpenStoreMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer closeAll(database)
	for pragmaName, want := range map[string]string{"journal_mode": "memory", "foreign_keys": "1", "busy_timeout": "10000"} {
		if got := pragma(t, database, pragmaName); got != want {
			t.Errorf("PRAGMA %s = %q, want %q", pragmaName, got, want)
		}
	}
	if got := database.Stats().MaxOpenConnections; got != 1 {
		t.Errorf("MaxOpenConnections = %d, want 1", got)
	}
	if _, err := database.ExecContext(ctx, "CREATE TABLE kept(v INTEGER); INSERT INTO kept VALUES(7)"); err != nil {
		t.Fatal(err)
	}
	second, err := sql.Open("sqlite", name)
	if err != nil {
		t.Fatal(err)
	}
	defer closeAll(second)
	var kept int
	if err := second.QueryRowContext(ctx, "SELECT v FROM kept").Scan(&kept); err != nil || kept != 7 {
		t.Fatalf("second handle on %s read %d, %v; want 7", name, kept, err)
	}
	other, otherName, err := sqlitedb.OpenStoreMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer closeAll(other)
	if otherName == name {
		t.Fatalf("two OpenStoreMemory calls share the name %s", name)
	}
	const missing = "SQL logic error: no such table: kept (1)"
	if err := other.QueryRowContext(ctx, "SELECT v FROM kept").Scan(&kept); err == nil || err.Error() != missing {
		t.Fatalf("other database read kept: %v, want %q", err, missing)
	}
	for path, want := range map[string]bool{
		name:                           true,
		otherName:                      true,
		"file:/pfm-memory-0?vfs=memdb": false,
		filepath.Join(dir, "pfm.db"):   false,
	} {
		if got := sqlitedb.InMemory(path); got != want {
			t.Errorf("InMemory(%q) = %v, want %v", path, got, want)
		}
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("working directory %s holds %v (%v), want nothing", dir, entries, err)
	}
}

// TestForeignOpenersKeepTheOwnersSettings pins the other program's store:
// read-only refuses a write, read-write can write, both carry the caller's
// busy timeout, and neither switches the owner's rollback journal to WAL.
func TestForeignOpenersKeepTheOwnersSettings(t *testing.T) {
	dir := t.TempDir()
	testjail.DatabaseFiles(t, dir)
	path := filepath.Join(dir, "owner.db")
	owner, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec("CREATE TABLE threads (id TEXT)"); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	readOnly, err := sqlitedb.OpenReadOnly(path, 2*time.Second)
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
	readWrite, err := sqlitedb.OpenReadWrite(path, 5*time.Second)
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
// text: a home or PFM_STATE_DB directory holding "?", "#" or "%41" must open
// that very file — never a truncated or percent-decoded neighbor, which would
// read as an empty database.
func TestOpenersReachAPathWithURICharacters(t *testing.T) {
	dir := t.TempDir()
	testjail.DatabaseFiles(t, dir)
	path := filepath.Join(dir, "odd?dir#%41", "store.db")
	store, err := sqlitedb.OpenStore(context.Background(), path)
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
		"read-only": sqlitedb.OpenReadOnly, "read-write": sqlitedb.OpenReadWrite,
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

// TestBusyNamesOnlySQLiteBusy pins the one busy door: a write refused by another connection's write
// lock is busy, wrapped or not; a missing table, a plain error and no error are not.
func TestBusyNamesOnlySQLiteBusy(t *testing.T) {
	dir := t.TempDir()
	testjail.DatabaseFiles(t, dir)
	path := filepath.Join(dir, "busy.db")
	holder, err := sqlitedb.OpenReadWrite(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Close() }()
	if _, err := holder.Exec("CREATE TABLE rows (id TEXT)"); err != nil {
		t.Fatal(err)
	}
	tx, err := holder.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec("INSERT INTO rows VALUES ('held')"); err != nil {
		t.Fatal(err)
	}
	other, err := sqlitedb.OpenReadWrite(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	_, busy := other.Exec("INSERT INTO rows VALUES ('refused')")
	_, missing := other.Exec("SELECT * FROM no_such_table")
	for _, row := range []struct {
		name string
		err  error
		want bool
	}{
		{"a write under another's lock", busy, true},
		{"the same, wrapped", fmt.Errorf("record: %w", busy), true},
		{"a missing table", missing, false},
		{"a plain error", errors.New("busy"), false},
		{"no error", nil, false},
	} {
		if got := sqlitedb.Busy(row.err); got != row.want {
			t.Errorf("Busy(%s: %v) = %v, want %v", row.name, row.err, got, row.want)
		}
	}
}
