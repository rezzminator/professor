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

	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

// v3Schema is the schema a version-3 pfm leaves: hidden has no revision.
var v3Schema = strings.Replace(
	schemaDDL,
	"'call'",
	"'inject'",
	1,
) + migrationV2 + migrationV3 + "PRAGMA user_version=3;"

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

func TestSharedOpenUpgradesSupportedSchemasAndPreservesKills(t *testing.T) {
	t.Parallel()
	for _, version := range []int{0, 1, 3, 4} {
		t.Run(strconv.Itoa(version), func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "pfm.db")
			if version != 0 {
				seed := schemaDDL
				if version >= 3 {
					seed = v3Schema
				}
				seedSharedState(
					t,
					path,
					seed+fmt.Sprintf(
						"INSERT INTO hidden(uuid,hidden_at) VALUES('kept',77); PRAGMA user_version=%d;",
						version,
					),
				)
			}
			state := OpenSharedState(ctx, paths.Values{StateDB: path})
			if err := state.Degraded(); err != nil {
				t.Fatal(err)
			}
			revision, err := state.KillWithRevision(ctx, "new", 90)
			if err != nil || revision == "" {
				t.Fatalf("current kill=%q,%v", revision, err)
			}
			var got int
			if err := state.db.QueryRow("PRAGMA user_version").Scan(&got); err != nil || got != SchemaVersion {
				t.Fatalf("version=%d err=%v", got, err)
			}
			if err := state.Close(); err != nil {
				t.Fatal(err)
			}
			again := OpenSharedState(ctx, paths.Values{StateDB: path})
			defer func() {
				if err := again.Close(); err != nil {
					t.Error(err)
				}
			}()
			want := map[string]int64{"new": 90}
			if version != 0 {
				want["kept"] = 77
			}
			kills, err := again.KilledAt(ctx)
			if err != nil || !reflect.DeepEqual(kills, want) {
				t.Fatalf("preserved current kills=%v,%v want%v", kills, err, want)
			}
		})
	}
}

func TestSharedOpenRepairsMovedTmuxSeatsWithoutSchemaBump(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "pfm.db")
	values := paths.Values{StateDB: path}
	seed := OpenSharedState(ctx, values)
	for _, seat := range []Seat{
		{SeatID: "tmux-s1", SessionID: "S1", Backend: "tmux", State: "live", UpdatedAt: 100},
		{SeatID: "herdr-s1", SessionID: "S1", Backend: "herdr", State: "live", HerdrSocket: "/h.sock", HerdrPane: "w1:p1", UpdatedAt: 200},
		{SeatID: "tmux-s2", SessionID: "S2", Backend: "tmux", State: "live", UpdatedAt: 300},
		{SeatID: "herdr-s2", SessionID: "S2", Backend: "herdr", State: "live", HerdrSocket: "/h.sock", HerdrPane: "w1:p2", UpdatedAt: 200},
		{SeatID: "tmux-s3", SessionID: "S3", Backend: "tmux", State: "live", UpdatedAt: 100},
		{SeatID: "herdr-s3", SessionID: "S3", Backend: "herdr", State: "lost", HerdrSocket: "/h.sock", HerdrPane: "w1:p3", UpdatedAt: 200},
		{SeatID: "tmux-empty", Backend: "tmux", State: "live"},
	} {
		if _, err := seed.db.ExecContext(ctx,
			"INSERT INTO seat ("+seatColumns+") VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
			seatValues(seat)...,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	state := OpenSharedState(ctx, values)
	if err := state.Degraded(); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		id   string
		want bool
	}{{"tmux-s1", false}, {"tmux-s2", true}, {"tmux-s3", true}, {"tmux-empty", true}} {
		if _, found, err := state.Seat(ctx, test.id); err != nil || found != test.want {
			t.Errorf("seat %s found=%v err=%v want %v", test.id, found, err, test.want)
		}
	}
	var version int
	if err := state.db.QueryRowContext(ctx, "PRAGMA user_version").
		Scan(&version); err != nil ||
		version != SchemaVersion {
		t.Fatalf("user_version=%d err=%v want %d", version, err, SchemaVersion)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	release, err := LockMigration(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	waiting, cancel := context.WithTimeout(ctx, time.Second)
	again := OpenSharedState(waiting, values)
	openedErr := again.Degraded()
	closeErr := again.Close()
	cancel()
	releaseErr := release()
	if err := errors.Join(openedErr, closeErr, releaseErr); err != nil {
		t.Fatalf("second clean open took migration lock: %v", err)
	}
	state = OpenSharedState(ctx, values)
	if err := state.Degraded(); err != nil {
		t.Fatal(err)
	}
	if err := state.PutSeat(
		ctx,
		Seat{SeatID: "tmux-s1", SessionID: "S1", Backend: "tmux", State: "live", UpdatedAt: 300},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := state.db.ExecContext(ctx, "UPDATE seat SET updated_at=100 WHERE seat_id='tmux-s1'"); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".migrate.lock"); !os.IsNotExist(err) {
		t.Fatalf("second open left/took migration lock: %v", err)
	}

	readonly := OpenSharedStateReadOnly(ctx, values)
	defer func() { _ = readonly.Close() }()
	if err := readonly.Degraded(); err != nil {
		t.Fatal(err)
	}
	if _, found, err := readonly.Seat(ctx, "tmux-s1"); err != nil || !found {
		t.Fatalf("read-only open repaired stale tmux row: found=%v err=%v", found, err)
	}
}

// A moved-seat repair that cannot run keeps the stale row and the store open:
// it warns and repairs at the next open. Serial: obs.Test.
func TestSharedOpenKeepsMovedTmuxSeatsWhenTheirRepairFails(t *testing.T) {
	ctx, recorder := obs.Test(t)
	path := filepath.Join(t.TempDir(), "pfm.db")
	values := paths.Values{StateDB: path}
	seed := OpenSharedState(ctx, values)
	for _, seat := range []Seat{
		{SeatID: "tmux-s1", SessionID: "S1", Backend: "tmux", State: "live", UpdatedAt: 100},
		{SeatID: "herdr-s1", SessionID: "S1", Backend: "herdr", State: "live", HerdrSocket: "/h.sock", HerdrPane: "w1:p1", UpdatedAt: 200},
	} {
		if _, err := seed.db.ExecContext(ctx,
			"INSERT INTO seat ("+seatColumns+") VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
			seatValues(seat)...,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	// A directory where the migration lock file goes makes the lock unopenable.
	if err := os.Mkdir(path+".migrate.lock", 0o700); err != nil {
		t.Fatal(err)
	}
	state := OpenSharedState(ctx, values)
	defer func() { _ = state.Close() }()
	if err := state.Degraded(); err != nil {
		t.Fatalf("open with an unrunnable repair: %v, want the store open", err)
	}
	if _, found, err := state.Seat(ctx, "tmux-s1"); err != nil || !found {
		t.Fatalf("stale tmux seat found=%v err=%v, want it kept", found, err)
	}
	if got, want := openWarnings(recorder, "fleet.seats.repair_deferred"),
		[]string{
			"open migration lock " + path + ".migrate.lock: open " + path + ".migrate.lock: is a directory",
		}; !reflect.DeepEqual(got, want) {
		t.Fatalf("repair warnings = %q, want %q", got, want)
	}
}

// openWarnings lists the err text of every WARN record named event.
func openWarnings(recorder *obs.Recorder, event string) []string {
	var texts []string
	for _, record := range recorder.Records() {
		if record.Message == event && record.Level == "WARN" {
			text, _ := record.Field(obs.FieldErr)
			texts = append(texts, text.(string))
		}
	}
	return texts
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
			seed: v3Schema + migrationV5 + migrationV6 + fmt.Sprintf("PRAGMA user_version=%d;", SchemaVersion) + hiddenRevisionDDL + ";" + schedulesRenameDDL + ";" + scheduleOnceDDL + ";",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "pfm.db")
			seedSharedState(t, path, test.seed)
			if !test.waits {
				current := OpenSharedState(ctx, paths.Values{StateDB: path})
				if err := errors.Join(current.Degraded(), current.Close()); err != nil {
					t.Fatal(err)
				}
			}
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

// retiredIssuesSeed is a version-6 file still holding the retired issues ledger.
func retiredIssuesSeed(rows int) string {
	seed := v3Schema + migrationV5 + migrationV6 + hiddenRevisionDDL + ";" + schedulesRenameDDL + ";" +
		scheduleOnceDDL + ";" + `
CREATE TABLE issues(id INTEGER PRIMARY KEY, at_ns INTEGER NOT NULL, title TEXT NOT NULL, detail TEXT NOT NULL);
CREATE INDEX issues_at ON issues(at_ns);
INSERT INTO hidden(uuid,hidden_at) VALUES('keep',77);
PRAGMA user_version=6;`
	for row := range rows {
		seed += fmt.Sprintf("INSERT INTO issues(at_ns,title,detail) VALUES(%d,'filed','kept in the backup');", row)
	}
	return seed
}

func countIssueObjects(t *testing.T, db *sql.DB) int {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name LIKE 'issues%'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// A row filed after the backup, by an older pfm still running, keeps the table:
// the drop recounts under its write lock and drops only the backed-up count.
func TestSharedIssuesDropRefusesARowFiledAfterItsBackup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "pfm.db")
	seedSharedState(t, path, retiredIssuesSeed(1))
	db, err := sqlitedb.OpenReadWrite(path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	dropped, err := dropIssuesHolding(ctx, db, path, 0)
	if err != nil || dropped {
		t.Fatalf("drop with one unbacked row: dropped=%v err=%v, want kept", dropped, err)
	}
	if count := countIssueObjects(t, db); count != 2 {
		t.Fatalf("issues objects=%d after a changed count, want the table and index kept", count)
	}
	if dropped, err := dropIssuesHolding(ctx, db, path, 1); err != nil || !dropped {
		t.Fatalf("drop at the backed-up count: dropped=%v err=%v, want dropped", dropped, err)
	}
	if count := countIssueObjects(t, db); count != 0 {
		t.Fatalf("issues objects=%d after the drop, want none", count)
	}
}

// A backup that cannot be written leaves the ledger in place and the store
// open: the cleanup warns and retries at the next open. Serial: obs.Test.
func TestSharedOpenKeepsTheIssuesTableWhenItsBackupFails(t *testing.T) {
	ctx, recorder := obs.Test(t)
	path := filepath.Join(t.TempDir(), "pfm.db")
	seedSharedState(t, path, retiredIssuesSeed(1))
	if err := os.Mkdir(path+".bak-before-issues-drop", 0o700); err != nil {
		t.Fatal(err)
	}
	state := OpenSharedState(ctx, paths.Values{StateDB: path})
	err := state.Degraded()
	if closeErr := state.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatalf("open with an unwritable backup: %v, want the store open", err)
	}
	if got, want := openWarnings(recorder, "fleet.issues.kept"),
		[]string{"keep retired issues before their drop: backup " + path + ": " + path +
			".bak-before-issues-drop is not a regular file"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("issues warnings = %q, want %q", got, want)
	}
	db, openErr := sqlitedb.OpenReadOnly(path, time.Second)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer func() { _ = db.Close() }()
	if count := countIssueObjects(t, db); count != 2 {
		t.Fatalf("issues objects=%d after a failed backup, want the table and index kept", count)
	}
}

// A read-only open never drops the ledger.
func TestSharedReadOnlyOpenLeavesTheIssuesTable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "pfm.db")
	seedSharedState(t, path, retiredIssuesSeed(1))
	state := OpenSharedStateReadOnly(ctx, paths.Values{StateDB: path})
	if err := state.Degraded(); err != nil {
		t.Fatal(err)
	}
	if _, err := state.KilledAt(ctx); err != nil {
		t.Fatal(err)
	}
	if count := countIssueObjects(t, state.db); count != 2 {
		t.Fatalf("issues objects=%d after a read-only open, want them kept", count)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
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
