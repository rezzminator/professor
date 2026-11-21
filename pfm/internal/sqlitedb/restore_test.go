package sqlitedb_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestRestoreFromReplacesTheDatabaseUnderAnOpenConnection(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	testjail.DatabaseFiles(t, dir)
	source, target := filepath.Join(dir, "source.db"), filepath.Join(dir, "target.db")
	restoreTestExec(
		t,
		source,
		"CREATE TABLE kept (v TEXT)",
		"INSERT INTO kept VALUES ('before')",
		"PRAGMA user_version=3",
	)
	restoreTestExec(t, target, "CREATE TABLE newer (v TEXT)", "PRAGMA user_version=4")
	destination := restoreTestOpen(t, target)
	other := restoreTestOpen(t, target)
	if got := restoreTestVersion(t, other); got != 4 {
		t.Fatalf("%s user_version before restore = %d, want 4", target, got)
	}
	if err := sqlitedb.RestoreFrom(ctx, destination, source); err != nil {
		t.Fatalf("RestoreFrom(%s from %s) = %v, want nil", target, source, err)
	}
	if got := restoreTestVersion(t, other); got != 3 {
		t.Fatalf("%s user_version read by another connection after restore = %d, want 3", target, got)
	}
	var kept string
	if err := other.QueryRowContext(ctx, "SELECT v FROM kept").Scan(&kept); err != nil || kept != "before" {
		t.Fatalf("%s kept row after restore = %q, %v; want %q, nil", target, kept, err, "before")
	}
	var newer int
	query := "SELECT count(*) FROM sqlite_master WHERE name = 'newer'"
	if err := other.QueryRowContext(ctx, query).Scan(&newer); err != nil || newer != 0 {
		t.Fatalf("%s tables named newer after restore = %d, %v; want 0, nil", target, newer, err)
	}
}

func restoreTestOpen(t *testing.T, path string) *sql.DB {
	t.Helper()
	database, err := sqlitedb.OpenStore(context.Background(), path)
	if err != nil {
		t.Fatalf("OpenStore(%s) = %v", path, err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close %s: %v", path, err)
		}
	})
	return database
}

func restoreTestExec(t *testing.T, path string, statements ...string) {
	t.Helper()
	database := restoreTestOpen(t, path)
	for _, statement := range statements {
		if _, err := database.ExecContext(context.Background(), statement); err != nil {
			t.Fatalf("%s: %s: %v", path, statement, err)
		}
	}
}

func restoreTestVersion(t *testing.T, database *sql.DB) int {
	t.Helper()
	var version int
	if err := database.QueryRowContext(context.Background(), "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	return version
}
