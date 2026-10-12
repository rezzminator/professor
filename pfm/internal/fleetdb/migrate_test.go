package fleetdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

// The statements a pfm built from b5604e2c (SchemaVersion 3) runs on hidden,
// verbatim from its fleetdb.go: Kill, KillUntilPrompt, Unkill, ReassertKill,
// UnkillIfPayload and KilledRecords. Its open refuses a file whose
// user_version is newer than 3.
const (
	v3SchemaVersion = 3
	v3Kill          = `
INSERT INTO hidden(uuid,hidden_at,at_payload) VALUES(?,?,NULL)
ON CONFLICT(uuid) DO UPDATE SET
  hidden_at=excluded.hidden_at,
  at_payload=NULL`
	v3KillUntilPrompt = `
INSERT INTO hidden(uuid,hidden_at,at_payload) VALUES(?,?,?)
ON CONFLICT(uuid) DO UPDATE SET
  hidden_at=CASE WHEN hidden.at_payload IS NULL THEN hidden.hidden_at ELSE excluded.hidden_at END,
  at_payload=CASE
    WHEN hidden.at_payload IS NULL THEN NULL
    WHEN excluded.at_payload > hidden.at_payload THEN excluded.at_payload
    ELSE hidden.at_payload
  END`
	v3Unkill          = "DELETE FROM hidden WHERE uuid=?"
	v3ReassertKill    = "UPDATE hidden SET at_payload=NULL WHERE uuid=?"
	v3UnkillIfPayload = "DELETE FROM hidden WHERE uuid=? AND at_payload=?"
	v3KilledRecords   = "SELECT uuid, hidden_at, at_payload FROM hidden"
)

// v3Schema is the schema a version-3 pfm leaves: hidden has no revision.
var v3Schema = schemaDDL + migrationV2 + migrationV3 + "PRAGMA user_version=3;"

// seedSharedState writes a database file the way an older pfm left it.
func seedSharedState(t *testing.T, path, script string) {
	t.Helper()
	db, err := sqlitedb.OpenReadWrite(path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(script); err != nil {
		t.Fatal(errors.Join(err, db.Close()))
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

// v3Exec runs one version-3 statement and returns the rows it changed.
func v3Exec(t *testing.T, db *sql.DB, query string, args ...any) int64 {
	t.Helper()
	result, err := db.Exec(query, args...)
	if err != nil {
		t.Fatalf("version-3 statement %q%v: %v", query, args, err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		t.Fatalf("version-3 statement %q rows: %v", query, err)
	}
	return changed
}

// v3Kills reads hidden the way a version-3 pfm lists kills: id → hidden_at,
// with -1 for a clear-kill baseline still pending.
func v3Kills(t *testing.T, db *sql.DB) map[string]int64 {
	t.Helper()
	rows, err := db.Query(v3KilledRecords)
	if err != nil {
		t.Fatalf("version-3 kill list: %v", err)
	}
	kills := map[string]int64{}
	for rows.Next() {
		var id string
		var killedAt int64
		var payload sql.NullInt64
		if err := rows.Scan(&id, &killedAt, &payload); err != nil {
			t.Fatal(errors.Join(err, rows.Close()))
		}
		if payload.Valid {
			killedAt = -1
		}
		kills[id] = killedAt
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	return kills
}

// TestSharedStateStaysOpenableByAVersion3Pfm: whatever file this build opens —
// none yet, version 1, the version-3 file an installed pfm uses, or the
// version-4 file a pre-fix develop build left — it leaves a user_version a
// b5604e2c pfm accepts, and that pfm's statements on hidden still succeed
// against it, on a connection it opened before this build's open as a running
// MCP server or picker holds one; this build then reads back every kill the
// older pfm wrote.
func TestSharedStateStaysOpenableByAVersion3Pfm(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		seed string // empty: no file yet
		kept bool   // the seed holds the kill "kept" at 77
	}{
		{name: "no file yet"},
		{name: "version 1", seed: schemaDDL + "INSERT INTO hidden(uuid,hidden_at) VALUES('kept',77);", kept: true},
		{
			name: "version 3, an installed pfm's file",
			seed: v3Schema + "INSERT INTO hidden(uuid,hidden_at) VALUES('kept',77);",
			kept: true,
		},
		{
			name: "version 4, a pre-fix develop build's file",
			seed: v3Schema + `ALTER TABLE hidden ADD COLUMN revision TEXT NOT NULL DEFAULT '';
INSERT INTO hidden(uuid,hidden_at,revision) VALUES('kept',77,'pre-fix');
PRAGMA user_version=4;`,
			kept: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "pfm.db")
			db, err := sqlitedb.OpenReadWrite(path, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			}()
			if test.seed != "" {
				seedSharedState(t, path, test.seed)
				v3Kills(t, db) // the older pfm's connection has read hidden before this build opens
			}
			state := OpenSharedState(ctx, paths.Values{StateDB: path})
			if err := state.Degraded(); err != nil {
				t.Fatalf("this build's open: %v", err)
			}
			revision, err := state.KillWithRevision(ctx, "this-build", 90)
			if err := errors.Join(err, state.Close()); err != nil || revision == "" {
				t.Fatalf("this build's kill = %q, %v; want a rollback revision", revision, err)
			}

			var version int
			if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
				t.Fatal(err)
			}
			if version > v3SchemaVersion {
				t.Fatalf("a version-3 pfm refuses this file: schema version %d is newer than binary version %d",
					version, v3SchemaVersion)
			}
			for _, step := range []struct {
				query string
				args  []any
			}{
				{v3Kill, []any{"v3-kill", 100}},
				{v3Kill, []any{"this-build", 101}},
				{v3KillUntilPrompt, []any{"v3-clear", 102, 5}},
				{v3ReassertKill, []any{"v3-clear"}},
				{v3KillUntilPrompt, []any{"v3-expired", 103, 6}},
				{v3UnkillIfPayload, []any{"v3-expired", 6}},
				{v3Unkill, []any{"v3-kill"}},
			} {
				if changed := v3Exec(t, db, step.query, step.args...); changed != 1 {
					t.Fatalf("version-3 statement %q%v changed %d rows, want 1", step.query, step.args, changed)
				}
			}
			want := map[string]int64{"this-build": 101, "v3-clear": 102}
			if test.kept {
				want["kept"] = 77
			}
			if got := v3Kills(t, db); !reflect.DeepEqual(got, want) {
				t.Fatalf("version-3 kill list = %v, want %v", got, want)
			}

			again := OpenSharedState(ctx, paths.Values{StateDB: path})
			if err := again.Degraded(); err != nil {
				t.Fatalf("this build's reopen: %v", err)
			}
			defer func() {
				if err := again.Close(); err != nil {
					t.Error(err)
				}
			}()
			records, err := again.KilledAt(ctx)
			if err != nil || !reflect.DeepEqual(records, want) {
				t.Fatalf("this build's kills = %v, %v; want %v", records, err, want)
			}
			var check string
			if err := again.db.QueryRow("PRAGMA quick_check").Scan(&check); err != nil || check != "ok" {
				t.Fatalf("quick_check = %q, %v", check, err)
			}
		})
	}
}

// TestSharedOpenMigratesAtOpenUnderTheMigrationLock: an open that must add
// hidden.revision or rename reminders to schedules waits for the migration
// lock, so two first openers never both apply it; a file that already has both
// opens without the lock.
func TestSharedOpenMigratesAtOpenUnderTheMigrationLock(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		seed  string
		waits bool
	}{
		{name: "missing column waits for the lock", seed: v3Schema, waits: true},
		{
			name: "a current file opens without it",
			seed: v3Schema + hiddenRevisionDDL + ";" + schedulesRenameDDL + ";" + scheduleOnceDDL + ";",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "pfm.db")
			seedSharedState(t, path, test.seed)
			release, err := LockMigration(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			// A held lock never releases, so only the configure step races this deadline.
			waiting, cancel := context.WithTimeout(ctx, 2*time.Second)
			held := OpenSharedState(waiting, paths.Values{StateDB: path})
			cancel()
			heldErr := held.Degraded()
			if err := errors.Join(held.Close(), release()); err != nil {
				t.Fatal(err)
			}
			waited := heldErr != nil && strings.Contains(heldErr.Error(), "wait for migration lock")
			if waited != test.waits {
				t.Fatalf("open under a held migration lock: %v; want waited=%v", heldErr, test.waits)
			}
			state := OpenSharedState(ctx, paths.Values{StateDB: path})
			if err := state.Degraded(); err != nil {
				t.Fatalf("open after release: %v", err)
			}
			defer func() {
				if err := state.Close(); err != nil {
					t.Error(err)
				}
			}()
			if columns := queryColumn(t, state, "SELECT name FROM pragma_table_info('hidden')"); !reflect.DeepEqual(
				columns, []string{"uuid", "hidden_at", "at_payload", "revision"},
			) {
				t.Fatalf("hidden columns = %v", columns)
			}
		})
	}
}

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
	newer := RetiredSchemaVersion + 1
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
