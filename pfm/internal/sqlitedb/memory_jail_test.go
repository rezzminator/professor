package sqlitedb_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
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
