package testjail

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

// TestDatabaseFilesKeepsSQLitesOwnFile pins the allowance: a store under the
// allowed directory, nested or not or named relative to it as the working
// directory, is SQLite's own WAL file and AttachName
// names its path; a sibling directory sharing the name's prefix stays in
// memory, and the allowance ends with the test that asked for it. It is a jail
// test: half of it is the file.
func TestDatabaseFilesKeepsSQLitesOwnFile(t *testing.T) {
	parent := t.TempDir()
	allowed := filepath.Join(parent, "a")
	var nested string
	t.Run("allowed", func(t *testing.T) {
		DatabaseFiles(t, allowed)
		for _, path := range []string{filepath.Join(allowed, "pfm.db"), filepath.Join(allowed, "deep", "index.db")} {
			if got := journalMode(t, path); got != "wal" {
				t.Errorf("%s journal_mode = %q, want wal", path, got)
			}
			header := make([]byte, 15)
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			_, err = file.Read(header)
			_ = file.Close()
			if err != nil || string(header) != "SQLite format 3" {
				t.Errorf("%s begins %q, %v; want SQLite format 3", path, header, err)
			}
			if got, err := sqlitedb.AttachName(path); err != nil || got != path {
				t.Errorf("AttachName(%s) = %q, %v; want the path itself", path, got, err)
			}
		}
		sibling := filepath.Join(parent, "ab", "pfm.db")
		if got := journalMode(t, sibling); got != "memory" {
			t.Errorf("%s journal_mode = %q, want memory: the allowance covers %s alone", sibling, got, allowed)
		}
		nested = filepath.Join(allowed, "later", "pfm.db")
	})
	t.Run("relative to an allowed working directory", func(t *testing.T) {
		DatabaseFiles(t, allowed)
		t.Chdir(allowed)
		if got := journalMode(t, filepath.Join("rel", "pfm.db")); got != "wal" {
			t.Errorf("rel/pfm.db under the allowed working directory %s: journal_mode %q, want wal", allowed, got)
		}
	})
	if got := journalMode(t, nested); got != "memory" {
		t.Errorf("%s after its allowance ended: journal_mode %q, want memory", nested, got)
	}
}

// journalMode opens path through the SQLite door and reads its journal mode,
// "wal" on SQLite's file and "memory" on a database the door serves.
func journalMode(t *testing.T, path string) string {
	t.Helper()
	database, err := sqlitedb.OpenStore(context.Background(), path)
	if err != nil {
		t.Fatalf("OpenStore(%s): %v", path, err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close %s: %v", path, err)
		}
	}()
	var mode string
	if err := database.QueryRowContext(context.Background(), "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("PRAGMA journal_mode on %s: %v", path, err)
	}
	return mode
}
