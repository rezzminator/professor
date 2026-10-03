package fleetdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestSharedMigrationFromV1PreservesRowsAndBacksUp(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "pfm.db")
	ctx := context.Background()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode=WAL").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode=%q,%v", mode, err)
	}
	if _, err := db.ExecContext(
		ctx,
		schemaDDL+`CREATE TABLE swap_event(id INTEGER PRIMARY KEY, detail TEXT); INSERT INTO swap_event(detail) VALUES('old'); INSERT INTO hidden(uuid,hidden_at) VALUES('keep',77);`,
	); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	state := OpenSharedState(ctx, paths.Values{StateDB: path})
	if err := state.Degraded(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := state.Close(); err != nil {
			t.Error(err)
		}
	}()
	var version int
	if err := state.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != SchemaVersion {
		t.Fatalf("version=%d err=%v", version, err)
	}
	var count int
	if err := state.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='swap_event'").
		Scan(&count); err != nil ||
		count != 0 {
		t.Fatalf("swap_event=%d err=%v", count, err)
	}
	if err := state.db.QueryRow("SELECT count(*) FROM launch").Scan(&count); err != nil || count != 0 {
		t.Fatalf("launch=%d err=%v", count, err)
	}
	if err := state.db.QueryRow("SELECT hidden_at FROM hidden WHERE uuid='keep'").
		Scan(&count); err != nil ||
		count != 77 {
		t.Fatalf("hidden row=%d err=%v", count, err)
	}
	backup, err := sql.Open("sqlite", path+".bak-before-v2")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := backup.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := backup.QueryRow("SELECT count(*) FROM swap_event").Scan(&count); err != nil || count != 1 {
		t.Fatalf("backup swap_event=%d err=%v", count, err)
	}
}

func TestSharedMigrationBackupFailureLeavesV1(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "pfm.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(schemaDDL); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path+".bak-before-v2", 0o700); err != nil {
		t.Fatal(err)
	}
	state := OpenSharedState(context.Background(), paths.Values{StateDB: path})
	if err := state.Degraded(); err == nil || !strings.Contains(err.Error(), "backup") {
		t.Fatalf("Degraded()=%v, want backup failure", err)
	}
	_ = state.Close()
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 0 {
		t.Fatalf("version=%d err=%v", version, err)
	}
}

func TestSharedMigrationReopenKeepsBackup(t *testing.T) {
	t.Parallel()
	state, values := openTestStore(t)
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	second := OpenSharedState(context.Background(), values)
	if err := second.Degraded(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := second.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := os.Stat(values.StateDB + ".bak-before-v2"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reopen backup=%v", err)
	}
}

func TestSharedMigrationRejectsNewerVersion(t *testing.T) {
	t.Parallel()
	state, values := openTestStore(t)
	if _, err := state.db.Exec(fmt.Sprintf("PRAGMA user_version=%d", SchemaVersion+1)); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	second := OpenSharedState(context.Background(), values)
	if err := second.Degraded(); err == nil || !strings.Contains(err.Error(), strconv.Itoa(SchemaVersion+1)) ||
		!strings.Contains(err.Error(), strconv.Itoa(SchemaVersion)) {
		t.Fatalf("Degraded()=%v, want versions %d and %d", err, SchemaVersion+1, SchemaVersion)
	}
	_ = second.Close()
}

// TestLockMigrationSerializesOpenersAndRemovesItsFile pins the cross-process
// guard: a second opener waits while the lock is held, gives up with its
// context, and acquires once the holder releases — which unlinks the lock file.
func TestLockMigrationSerializesOpenersAndRemovesItsFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "pfm.db")
	lockPath := path + ".migrate.lock"
	release, err := LockMigration(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("held lock file: %v", err)
	}
	waiting, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if second, err := LockMigration(waiting, path); err == nil {
		if releaseErr := second(); releaseErr != nil {
			t.Error(releaseErr)
		}
		t.Fatal("second LockMigration acquired a held lock")
	} else if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "wait for migration lock") {
		t.Fatalf("second LockMigration error = %v, want a context-bounded wait", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lockPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("released lock file: %v, want removed", err)
	}
	again, err := LockMigration(context.Background(), path)
	if err != nil {
		t.Fatalf("LockMigration after release: %v", err)
	}
	if err := again(); err != nil {
		t.Fatal(err)
	}
}
