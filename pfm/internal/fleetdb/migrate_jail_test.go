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
	"sync"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestSharedMigrationFromV1PreservesRowsAndBacksUp(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testjail.DatabaseFiles(t, root)
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

// A writable open drops the retired issues ledger with no schema bump, copying
// the file aside first when the table holds rows.
func TestSharedOpenDropsTheRetiredIssuesTable(t *testing.T) {
	t.Parallel()
	for _, rows := range []int{2, 0} {
		t.Run(fmt.Sprintf("rows=%d", rows), func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			dir := t.TempDir()
			testjail.DatabaseFiles(t, dir)
			path := filepath.Join(dir, "pfm.db")
			seedSharedState(t, path, retiredIssuesSeed(rows))
			state := OpenSharedState(ctx, paths.Values{StateDB: path})
			if err := state.Degraded(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := state.Close(); err != nil {
					t.Error(err)
				}
			}()
			var version, hidden int
			if err := state.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 6 {
				t.Fatalf("user_version=%d err=%v, want 6: the drop needs no bump", version, err)
			}
			if count := countIssueObjects(t, state.db); count != 0 {
				t.Fatalf("issues objects=%d, want the table and its index dropped", count)
			}
			if err := state.db.QueryRow("SELECT hidden_at FROM hidden WHERE uuid='keep'").
				Scan(&hidden); err != nil || hidden != 77 {
				t.Fatalf("hidden row=%d err=%v, want the kill kept", hidden, err)
			}
			backupPath := path + ".bak-before-issues-drop"
			if rows == 0 {
				if _, err := os.Stat(backupPath); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("empty ledger backup stat err=%v, want none written", err)
				}
				return
			}
			backup, err := sql.Open("sqlite", backupPath)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := backup.Close(); err != nil {
					t.Error(err)
				}
			}()
			var kept int
			if err := backup.QueryRow("SELECT count(*) FROM issues").Scan(&kept); err != nil || kept != rows {
				t.Fatalf("backup issues=%d err=%v, want %d", kept, err, rows)
			}
		})
	}
}

func TestSharedMigrationBackupFailureLeavesV1(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testjail.DatabaseFiles(t, root)
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
	state, values := openFileTestStore(t)
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
	state, values := openFileTestStore(t)
	newer := SchemaVersion + 1
	if _, err := state.db.Exec(fmt.Sprintf("PRAGMA user_version=%d", newer)); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	second := OpenSharedState(context.Background(), values)
	if err := second.Degraded(); err == nil || !strings.Contains(err.Error(), strconv.Itoa(newer)) ||
		!strings.Contains(err.Error(), strconv.Itoa(SchemaVersion)) {
		t.Fatalf("Degraded()=%v, want versions %d and %d", err, newer, SchemaVersion)
	}
	_ = second.Close()
}

func TestSharedMigrationRenamesStoredCalls(t *testing.T) {
	t.Parallel()
	for _, version := range []int{3, 4, 5} {
		t.Run(strconv.Itoa(version), func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			testjail.DatabaseFiles(t, dir)
			path := filepath.Join(dir, "pfm.db")
			seed := v3Schema + fmt.Sprintf("PRAGMA user_version=%d;", version) + `
INSERT INTO comms(id,at_ns,kind,sender_session,sender_label,sender_uuid,target,receiver_socket,receiver_pane,group_name,members,message)
VALUES(7,100,'inject','sender','Sender','uuid','target','socket','%1','group','members','full body');
INSERT INTO comms(id,at_ns,kind,message) VALUES(8,101,'group','group body'),(9,102,'spawn','spawn body');`
			wantBackupVersion, wantOriginalKind := 5, "inject"
			if version == 5 {
				seed += migrationV5 + "\nPRAGMA user_version=5;"
				wantBackupVersion, wantOriginalKind = 6, KindCall
			}
			seedSharedState(t, path, seed)
			state := OpenSharedState(ctx, paths.Values{StateDB: path})
			defer func() {
				if err := state.Close(); err != nil {
					t.Error(err)
				}
			}()
			if err := state.Degraded(); err != nil {
				t.Fatal(err)
			}
			rows, err := state.CommsSince(ctx, 0, 10)
			want := CommsEvent{
				ID:             7,
				AtNS:           100,
				Kind:           KindCall,
				SenderSession:  "sender",
				SenderLabel:    "Sender",
				SenderUUID:     "uuid",
				Target:         "target",
				ReceiverSocket: "socket",
				ReceiverPane:   "%1",
				GroupName:      "group",
				Members:        "members",
				Message:        "full body",
			}
			if err != nil || len(rows) != 3 || rows[2] != want || rows[1].Kind != "group" || rows[0].Kind != KindSpawn {
				t.Fatalf("migrated comms=%+v,%v want old row%+v", rows, err, want)
			}
			if err := state.RecordComms(ctx, CommsEvent{AtNS: 103, Kind: KindCall, Message: "new call"}); err != nil {
				t.Fatalf("new call after migration: %v", err)
			}
			rows, err = state.CommsSince(ctx, 103, 1)
			if err != nil || len(rows) != 1 || rows[0].Kind != KindCall || rows[0].Message != "new call" {
				t.Fatalf("new stored call=%+v,%v", rows, err)
			}
			var gotVersion int
			if err := state.db.QueryRow("PRAGMA user_version").
				Scan(&gotVersion); err != nil ||
				gotVersion != SchemaVersion {
				t.Fatalf("version=%d err=%v", gotVersion, err)
			}
			// The backup marks the first pending migration, not the latest schema version.
			backup, err := sqlitedb.OpenReadOnly(path+".bak-before-v"+strconv.Itoa(wantBackupVersion), time.Second)
			if err != nil {
				t.Fatalf("open backup before migration %d: %v", wantBackupVersion, err)
			}
			var originalKind string
			queryErr := backup.QueryRow("SELECT kind FROM comms WHERE id=7").Scan(&originalKind)
			if err := errors.Join(queryErr, backup.Close()); err != nil || originalKind != wantOriginalKind {
				t.Fatalf("original backup kind=%q want %q err=%v", originalKind, wantOriginalKind, err)
			}
		})
	}
}

// A reader's snapshot holds the WAL briefly while another opener reads the
// schema version; the backup waits it out instead of failing the open.
func TestBackupBeforeMigrationWaitsOutBusyCheckpoint(t *testing.T) {
	t.Parallel()
	db, path, release := heldBackupSnapshot(t)
	released := make(chan error, 1)
	go func() {
		time.Sleep(50 * time.Millisecond)
		released <- release()
	}()
	backupErr := BackupBeforeMigration(context.Background(), db, path, 7, true)
	if err := <-released; err != nil {
		t.Fatal(err)
	}
	if backupErr != nil {
		t.Fatalf("backup with a briefly held reader: %v", backupErr)
	}
	backup, err := sql.Open("sqlite", path+".bak-before-v7")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := backup.Close(); err != nil {
			t.Error(err)
		}
	}()
	var count int
	if err := backup.QueryRow("SELECT count(*) FROM t").Scan(&count); err != nil || count != 2 {
		t.Fatalf("backup rows=%d err=%v", count, err)
	}
}

func TestBackupBeforeMigrationBoundsBusyCheckpoint(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		wait time.Duration
		want string
	}{
		{name: "retry budget", wait: 6 * time.Second, want: "WAL checkpoint busy after"},
		{name: "caller deadline", wait: 50 * time.Millisecond, want: "wait for busy WAL checkpoint"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			db, path, _ := heldBackupSnapshot(t)
			// A checkpoint must not wait the store's ten seconds on every attempt.
			if _, err := db.Exec("PRAGMA busy_timeout=10000"); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), test.wait)
			defer cancel()
			err := BackupBeforeMigration(ctx, db, path, 7, true)
			if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("backup error=%v, want deadline with %q", err, test.want)
			}
			if test.name == "retry budget" && ctx.Err() != nil {
				t.Fatal("checkpoint exhausted the caller's deadline instead of its own retry budget")
			}
			var busyTimeout int
			if err := db.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout); err != nil || busyTimeout != 10000 {
				t.Fatalf("restored busy_timeout=%d err=%v", busyTimeout, err)
			}
			if _, err := os.Stat(path + ".bak-before-v7"); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed checkpoint published a backup: %v", err)
			}
		})
	}
}

// heldBackupSnapshot keeps frames newer than a second connection's snapshot in
// the WAL. Cleanup releases the reader even when the assertion fails.
func heldBackupSnapshot(t *testing.T) (*sql.DB, string, func() error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pfm.db")
	ctx := context.Background()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode=WAL").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode=%q,%v", mode, err)
	}
	if _, err := db.ExecContext(ctx, "CREATE TABLE t(v INTEGER); INSERT INTO t VALUES(1)"); err != nil {
		t.Fatal(err)
	}
	other, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := other.Close(); err != nil {
			t.Error(err)
		}
	})
	reader, err := other.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	var releaseErr error
	release := func() error {
		once.Do(func() {
			_, err := reader.ExecContext(ctx, "ROLLBACK")
			releaseErr = errors.Join(err, reader.Close())
		})
		return releaseErr
	}
	t.Cleanup(func() {
		if err := release(); err != nil {
			t.Error(err)
		}
	})
	if _, err := reader.ExecContext(ctx, "BEGIN"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := reader.QueryRowContext(ctx, "SELECT count(*) FROM t").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO t VALUES(2)"); err != nil {
		t.Fatal(err)
	}
	return db, path, release
}
