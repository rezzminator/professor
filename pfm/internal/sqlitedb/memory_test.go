package sqlitedb_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// markerLine is the one line the memory mode leaves at a database's path.
var markerLine = regexp.MustCompile(`^pfm memory database (file:/pfm-memory-\d+\?vfs=memdb)\n$`)

// openStore opens path through the door and closes the handle when the test ends.
func openStore(t *testing.T, path string) *sql.DB {
	t.Helper()
	database, err := sqlitedb.OpenStore(context.Background(), path)
	if err != nil {
		t.Fatalf("OpenStore(%s): %v", path, err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close %s: %v", path, err)
		}
	})
	return database
}

// readMarker returns the memory database name the marker at path holds.
func readMarker(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read marker %s: %v", path, err)
	}
	match := markerLine.FindStringSubmatch(string(content))
	if match == nil {
		t.Fatalf("%s holds %q, want the line %s", path, content, markerLine)
	}
	return match[1]
}

// TestOpenStoreServesAPathFromMemoryInATestProcess pins the test-process
// memory mode testjail.Run turns on through sqlitedb.ServeTestMemory: two opens of one path share one memory database whose marker
// sits at the path, the database outlives its handles until its home is
// released, and a marker naming a released database or a file the door did not
// write is refused by name, while an empty file is a new database.
func TestOpenStoreServesAPathFromMemoryInATestProcess(t *testing.T) {
	ctx := context.Background()
	root := testjail.Fleet(t)
	path := filepath.Join(root, "home", ".local", "state", "pfm", "pfm.db")
	first := openStore(t, path)
	second := openStore(t, path)
	if _, err := first.ExecContext(ctx, "CREATE TABLE kept(v INTEGER); INSERT INTO kept VALUES(7)"); err != nil {
		t.Fatal(err)
	}
	var kept int
	if err := second.QueryRowContext(ctx, "SELECT v FROM kept").Scan(&kept); err != nil || kept != 7 {
		t.Fatalf("second handle read %d, %v; want 7", kept, err)
	}
	if got := pragma(t, second, "journal_mode"); got != "memory" {
		t.Errorf("PRAGMA journal_mode = %q, want memory", got)
	}
	name := readMarker(t, path)
	if got, err := sqlitedb.AttachName(path); err != nil || got != name {
		t.Errorf("AttachName(%s) = %q, %v; want %q", path, got, err, name)
	}
	absent := filepath.Join(root, "index-absent.db")
	created, err := sqlitedb.AttachName(absent)
	if want := readMarker(t, absent); err != nil || created != want || created == name {
		t.Errorf(
			"AttachName(%s) = %q, %v; want a new database %q, as ATTACH creates a missing file",
			absent,
			created,
			err,
			want,
		)
	}
	memory, memoryName, err := sqlitedb.OpenStoreMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = memory.Close() }()
	if got, err := sqlitedb.AttachName(memoryName); err != nil || got != memoryName {
		t.Errorf("AttachName(%s) = %q, %v; want the memory store's own name", memoryName, got, err)
	}
	for _, database := range []*sql.DB{first, second} {
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := openStore(t, path).QueryRowContext(ctx, "SELECT v FROM kept").Scan(&kept); err != nil || kept != 7 {
		t.Fatalf("reopened %s read %d, %v; want 7", path, kept, err)
	}

	aside := filepath.Join(t.TempDir(), "aside.db")
	var released string
	t.Run("a home of its own", func(t *testing.T) {
		inner := filepath.Join(testjail.Fleet(t), "pfm.db")
		openStore(t, inner)
		released = readMarker(t, inner)
		content, err := os.ReadFile(inner)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(aside, content, 0o600); err != nil {
			t.Fatal(err)
		}
	})
	want := "sqlite database " + aside + " names memory database " + released + ", which this process no longer holds"
	_, err = sqlitedb.OpenStore(ctx, aside)
	if err == nil || err.Error() != want {
		t.Errorf("OpenStore(copied marker) error = %v, want %q", err, want)
	}
	if _, err := sqlitedb.AttachName(aside); err == nil || err.Error() != want {
		t.Errorf("AttachName(copied marker) error = %v, want %q", err, want)
	}

	dir := t.TempDir()
	foreign := filepath.Join(dir, "pfm.db")
	if err := os.WriteFile(foreign, []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = sqlitedb.OpenStore(ctx, foreign)
	if want := "sqlite database " + foreign + " is a file this test process did not write; " +
		"a test that needs SQLite's own file calls testjail.DatabaseFiles"; err == nil || err.Error() != want {
		t.Errorf("OpenStore(a file the door did not write) error = %v, want %q", err, want)
	}
	raced := filepath.Join(dir, "raced.db")
	var opens sync.WaitGroup
	for writer := range 8 {
		opens.Go(func() {
			database, err := sqlitedb.OpenStore(ctx, raced)
			if err == nil {
				_, err = database.ExecContext(
					ctx,
					"CREATE TABLE IF NOT EXISTS w(v INTEGER); INSERT INTO w VALUES(?)",
					writer,
				)
				err = errors.Join(err, database.Close())
			}
			if err != nil {
				t.Errorf("writer %d on %s: %v", writer, raced, err)
			}
		})
	}
	opens.Wait()
	var writers int
	if err := openStore(
		t,
		raced,
	).QueryRowContext(ctx, "SELECT count(*) FROM w").
		Scan(&writers); err != nil ||
		writers != 8 {
		t.Errorf("eight first opens of %s left %d rows, %v; want 8 in one database", raced, writers, err)
	}

	empty := filepath.Join(dir, "empty.db")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := pragma(t, openStore(t, empty), "journal_mode"); got != "memory" {
		t.Errorf("an empty file opened with journal_mode %q, want a new memory database", got)
	}
	readMarker(t, empty)
}
