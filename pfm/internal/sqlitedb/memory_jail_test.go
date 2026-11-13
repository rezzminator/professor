package sqlitedb_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestMemoryModeAnswersAsTheFile runs each open against SQLite's file (under
// testjail.DatabaseFiles) and against the test-process memory database and
// requires the answer a caller sees to be the same literal: the row read back,
// a write through a read-only handle, a read-only open of a missing path, and
// the busy error a second writer gets while the first holds BEGIN IMMEDIATE.
// It is a jail test: half of it is the file.
func TestMemoryModeAnswersAsTheFile(t *testing.T) {
	ctx := context.Background()
	rows := []struct {
		name string
		run  func(t *testing.T, dir string) string
		want string
	}{
		{"the row read back", func(t *testing.T, dir string) string {
			path := filepath.Join(dir, "pfm.db")
			if _, err := openStore(
				t,
				path,
			).ExecContext(ctx, "CREATE TABLE kept(v INTEGER); INSERT INTO kept VALUES(7)"); err != nil {
				return err.Error()
			}
			var kept int
			err := openStore(t, path).QueryRowContext(ctx, "SELECT v FROM kept").Scan(&kept)
			return fmt.Sprintf("%d %v", kept, err)
		}, "7 <nil>"},
		{"a write through a read-only handle", func(t *testing.T, dir string) string {
			path := filepath.Join(dir, "owner.db")
			if _, err := openStore(t, path).ExecContext(ctx, "CREATE TABLE threads(id TEXT)"); err != nil {
				return err.Error()
			}
			readOnly := openForeign(t, sqlitedb.OpenReadOnly, path, 2*time.Second)
			_, err := readOnly.ExecContext(ctx, "INSERT INTO threads VALUES ('a')")
			return fmt.Sprint(err)
		}, "attempt to write a readonly database (8)"},
		{"a read-only open of a missing path", func(t *testing.T, dir string) string {
			path := filepath.Join(dir, "absent.db")
			var count int
			err := openForeign(t, sqlitedb.OpenReadOnly, path, 2*time.Second).QueryRowContext(
				ctx, "SELECT count(*) FROM sqlite_master").Scan(&count)
			_, statErr := os.Stat(path)
			return fmt.Sprintf("%v; stat: %v", err, os.IsNotExist(statErr))
		}, "unable to open database file (14); stat: true"},
		{"a second writer while the first holds BEGIN IMMEDIATE", func(t *testing.T, dir string) string {
			path := filepath.Join(dir, "busy.db")
			holder := openForeign(t, sqlitedb.OpenReadWrite, path, 50*time.Millisecond)
			if _, err := holder.ExecContext(ctx, "CREATE TABLE rows(id TEXT)"); err != nil {
				return err.Error()
			}
			conn, err := holder.Conn(ctx)
			if err != nil {
				return err.Error()
			}
			defer func() { _ = conn.Close() }()
			if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
				return err.Error()
			}
			defer func() { _, _ = conn.ExecContext(ctx, "ROLLBACK") }()
			_, err = openForeign(t, sqlitedb.OpenReadWrite, path, 50*time.Millisecond).ExecContext(
				ctx, "INSERT INTO rows VALUES ('refused')")
			return fmt.Sprintf("%v; busy: %v", err, sqlitedb.Busy(err))
		}, "database is locked (5) (SQLITE_BUSY); busy: true"},
	}
	for _, row := range rows {
		for _, mode := range []string{"file", "memory"} {
			t.Run(row.name+" in "+mode, func(t *testing.T) {
				dir := t.TempDir()
				if mode == "file" {
					testjail.DatabaseFiles(t, dir)
				}
				if got := row.run(t, dir); got != row.want {
					t.Errorf("%s: %q, want %q", mode, got, row.want)
				}
			})
		}
	}
}

// openForeign opens path through a foreign opener and closes it when the test ends.
func openForeign(
	t *testing.T, open func(string, time.Duration) (*sql.DB, error), path string, busy time.Duration,
) *sql.DB {
	t.Helper()
	database, err := open(path, busy)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close %s: %v", path, err)
		}
	})
	return database
}

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
