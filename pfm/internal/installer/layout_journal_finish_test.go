package installer

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestRollbackRemovesTheMigrationBackupsTheInstallCreated(t *testing.T) {
	env := layoutFixture(t)
	env.StateDB = filepath.Join(env.Home, ".local", "state", "pfm", "pfm.db")
	env.CacheDB = filepath.Join(env.Home, ".local", "state", "pfm", "pfm-cache.db")
	layoutWrite(t, env.StateDB, "moved\n")
	journal := NewJournal(context.Background(), env)
	if err := journal.Write([]string{env.StateDB}, func() error {
		return os.WriteFile(env.StateDB, []byte("migrated\n"), 0o600)
	}); err != nil {
		t.Fatal(err)
	}
	before, err := journal.MigrationBackups()
	if err != nil {
		t.Fatal(err)
	}
	stateBackup := env.StateDB + ".bak-before-v2"
	cacheBackup := env.CacheDB + ".bak-before-v10"
	layoutWrite(t, stateBackup, "moved\n")
	layoutWrite(t, cacheBackup, "cache\n")
	if err := journal.JournalMigrationBackups(before); err != nil {
		t.Fatal(err)
	}
	if err := RollbackLayout(context.Background(), env, filepath.Base(journal.Dir()), false, io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{stateBackup, cacheBackup} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("rollback left the install's migration backup %s: %v", path, err)
		}
	}
	if got := readFixture(t, env.StateDB); got != "moved\n" {
		t.Fatalf("state database = %q, want the pre-install content", got)
	}
}

func TestJournalMigrationBackupsLeavesABackupThatPredatesTheInstall(t *testing.T) {
	env := layoutFixture(t)
	env.StateDB = filepath.Join(env.Home, ".local", "state", "pfm", "pfm.db")
	env.CacheDB = filepath.Join(env.Home, ".local", "state", "pfm", "pfm-cache.db")
	layoutWrite(t, env.StateDB, "moved\n")
	operator := env.StateDB + ".bak-before-v1"
	layoutWrite(t, operator, "an older backup\n")
	journal := NewJournal(context.Background(), env)
	if err := journal.Write([]string{env.StateDB}, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	before, err := journal.MigrationBackups()
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.JournalMigrationBackups(before); err != nil {
		t.Fatal(err)
	}
	if err := RollbackLayout(context.Background(), env, filepath.Base(journal.Dir()), false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := readFixture(t, operator); got != "an older backup\n" {
		t.Fatalf("rollback touched a backup that predates the install: %q", got)
	}
}
