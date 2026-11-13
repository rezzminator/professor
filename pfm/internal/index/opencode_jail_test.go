package index

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func openMirrorStore(t *testing.T) *store.Store {
	t.Helper()
	root := t.TempDir()
	t.Setenv(paths.EnvCacheDB, filepath.Join(root, "pfm-cache.db"))
	t.Setenv(paths.EnvStateDB, filepath.Join(root, "shared.db"))
	database, err := store.Open()
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	return database
}

// TestSyncOpenCodeMirrorsChangedStoresOnly pins the mirror pass a fleet scan
// runs on every refresh: a pass over an unchanged store neither reads it nor
// replaces the mirror, and a commit a live OpenCode left in its WAL (the
// database file untouched) is read, dropping the session it deleted.
func TestSyncOpenCodeMirrorsChangedStoresOnly(t *testing.T) {
	database := openMirrorStore(t)
	ctx, recorder := obs.Test(t)
	root := t.TempDir()
	testjail.DatabaseFiles(t, root) // the stamp reads the store file's size, mtime and -wal sibling
	seedOpenCodeStore(t, root)
	reads := func() int {
		count := 0
		for _, record := range recorder.Records() {
			if kind, _ := record.Field("kind"); record.Message == "db.statement" && kind == "opencode" {
				count++
			}
		}
		return count
	}
	roots := []string{root}
	pass := func(step string, wantReads, wantMirrored int, wantIDs ...string) {
		t.Helper()
		var counters Counters
		if err := SyncOpenCode(ctx, database, roots, &counters); err != nil {
			t.Fatalf("%s: sync: %v", step, err)
		}
		stored, err := database.OpenCodeSessions(ctx)
		if err != nil {
			t.Fatalf("%s: reread mirror: %v", step, err)
		}
		ids := make([]string, 0, len(stored))
		for _, row := range stored {
			ids = append(ids, row.ID)
		}
		sort.Strings(ids)
		if got := reads(); got != wantReads || counters.OpenCodeSessions != wantMirrored ||
			strings.Join(ids, ",") != strings.Join(wantIDs, ",") {
			t.Fatalf("%s: store reads %d, mirrored %d, mirror %v; want %d, %d, %v",
				step, got, counters.OpenCodeSessions, ids, wantReads, wantMirrored, wantIDs)
		}
	}
	// A live OpenCode holds its store open in WAL mode, its WAL created by its
	// first transaction, so its commits stay in the WAL until a checkpoint.
	live, err := sqlitedb.OpenReadWrite(filepath.Join(root, "opencode.db"), 5*time.Second)
	if err != nil {
		t.Fatalf("open the live store: %v", err)
	}
	live.SetMaxOpenConns(1)
	defer func() {
		if err := live.Close(); err != nil {
			t.Errorf("close the live store: %v", err)
		}
	}()
	if _, err := live.Exec("PRAGMA journal_mode=WAL"); err != nil {
		t.Fatalf("enable WAL: %v", err)
	}
	if _, err := live.Exec("SELECT count(*) FROM session"); err != nil {
		t.Fatalf("open the live WAL: %v", err)
	}

	pass("first pass", 2, 3, "ses_child", "ses_gone", "ses_root")
	pass("unchanged store", 2, 0, "ses_child", "ses_gone", "ses_root")
	if _, err := live.Exec("DELETE FROM session WHERE id = 'ses_child'"); err != nil {
		t.Fatalf("delete child: %v", err)
	}
	pass("a commit only the WAL holds", 4, 2, "ses_gone", "ses_root")

	// A second OpenCode data home (PFM_OPENCODE_ROOT lists several) adds its
	// sessions to the mirror beside the first root's, never replacing them.
	second := t.TempDir()
	seedOpenCodeStore(t, second)
	mutateOpenCodeStore(t, second, "DELETE FROM session WHERE id <> 'ses_root'")
	mutateOpenCodeStore(t, second, "UPDATE session SET id = 'ses_second' WHERE id = 'ses_root'")
	roots = append(roots, second)
	pass("a second root", 8, 3, "ses_gone", "ses_root", "ses_second")
}
