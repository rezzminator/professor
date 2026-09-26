package store

import (
	"context"
	"database/sql"
	"os/exec"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

// TestSharedKillsNeedNoBridgeInEitherDirection pins the shared-state boundary:
// a raw SQLite writer is visible through an already-open Go store, and a Go
// kill is immediately visible to a fresh SQLite reader.
func TestSharedKillsNeedNoBridgeInEitherDirection(t *testing.T) {
	sqlite3 := lookSQLite3(t)
	setStoreTestJail(t)
	database := openTestStore(t)
	t.Cleanup(func() { _ = database.Close() })
	ctx := context.Background()

	for _, transcript := range []Transcript{
		{UUID: "zsh-hid", Path: "/jail/zsh-hid.jsonl", Size: 10, MTimeNS: 2, PromptCount: 1},
		{UUID: "go-hid", Path: "/jail/go-hid.jsonl", Size: 10, MTimeNS: 1, PromptCount: 1},
	} {
		if err := database.UpsertTranscript(ctx, transcript); err != nil {
			t.Fatal(err)
		}
	}

	// External SQLite writer → Go. The store is open the whole time and is
	// never told anything happened.
	runSQLite3(t, sqlite3, database.SharedPath(), `
INSERT INTO hidden(uuid,hidden_at,at_payload) VALUES('zsh-hid',1700000000,NULL)
ON CONFLICT(uuid) DO UPDATE SET
  hidden_at=1700000000,
  at_payload=COALESCE(excluded.at_payload, hidden.at_payload);`)

	killed, err := database.KilledChats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(killed) != 1 || killed[0].ID != "zsh-hid" ||
		killed[0].Engine != pfmengine.Claude || killed[0].KilledAt != 1700000000 {
		t.Fatalf("KilledChats() after a CLI kill = %#v, want the zsh kill", killed)
	}
	transcripts, _, counts, err := database.DefaultCandidates(ctx, 30, 15)
	if err != nil {
		t.Fatal(err)
	}
	for _, transcript := range transcripts {
		if transcript.UUID == "zsh-hid" {
			t.Fatal("the cached first frame still lists an externally killed chat")
		}
	}
	if counts.Killed != 1 {
		t.Fatalf("cached killed count = %d, want 1", counts.Killed)
	}

	// Go → shared SQLite.
	if err := database.Kill(ctx, Killed{ID: "go-hid", KilledAt: 1700000001}); err != nil {
		t.Fatal(err)
	}

	// Go → a fresh external SQLite reader.
	listed := runSQLite3(
		t,
		sqlite3,
		database.SharedPath(),
		"SELECT uuid FROM hidden ORDER BY hidden_at DESC;",
	)
	if listed != "go-hid\nzsh-hid\n" {
		t.Fatalf("external killed listing = %q, want both kills", listed)
	}

	// And an unkill clears the authoritative row.
	if err := database.Unkill(ctx, "go-hid"); err != nil {
		t.Fatal(err)
	}
	if listed = runSQLite3(
		t,
		sqlite3,
		database.SharedPath(),
		"SELECT uuid FROM hidden ORDER BY uuid;",
	); listed != "zsh-hid\n" {
		t.Fatalf("killed rows after unkill = %q", listed)
	}
}

// The one-time adoption unions the retired local table into shared SQLite.
// The v9 backup preserves the retired rows for rollback.
func TestAdoptingLocalKillsUnionsOnceAndDeletesNothing(t *testing.T) {
	cachePath := setStoreTestJail(t)
	ctx := context.Background()
	cache, err := sqlitedb.OpenStore(ctx, cachePath)
	if err != nil {
		t.Fatal(err)
	}
	for version := 1; version <= 8; version++ {
		if _, err := cache.ExecContext(ctx, migrations[version-1]); err != nil {
			t.Fatalf("v%d: %v", version, err)
		}
		if _, err := cache.ExecContext(ctx, "PRAGMA user_version="+string(rune('0'+version))); err != nil {
			t.Fatal(err)
		}
	}
	// This row has the retired cache shape and has not yet been adopted.
	if _, err := cache.ExecContext(ctx, `
INSERT INTO hidden(id, engine, hidden_at, baseline_prompts)
VALUES ('cache-kill', 'cc', 4242, 9)`); err != nil {
		t.Fatal(err)
	}
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}

	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	shared := fleetdb.OpenSharedState(ctx, values)
	if err := shared.Kill(ctx, "shared-kill", 3000); err != nil {
		t.Fatal(err)
	}
	if err := shared.Close(); err != nil {
		t.Fatal(err)
	}

	first := openTestStore(t)
	killed, err := first.KilledChats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(killed) != 2 ||
		killed[0].ID != "cache-kill" || killed[0].KilledAt != 4242 ||
		killed[1].ID != "shared-kill" || killed[1].KilledAt != 3000 {
		t.Fatalf("adopted kills = %#v", killed)
	}
	var tableCount int
	if err := first.db.QueryRowContext(ctx,
		"SELECT count(*) FROM sqlite_master WHERE type='table' AND name='hidden'",
	).Scan(&tableCount); err != nil {
		t.Fatal(err)
	}
	if tableCount != 0 {
		t.Fatalf("retired cache hidden tables = %d, want 0", tableCount)
	}
	backup, err := sql.Open("sqlite", cachePath+".bak-before-v9")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backup.Close(); err != nil {
			t.Errorf("close cache backup: %v", err)
		}
	})
	var backupCount int
	if err := backup.QueryRowContext(ctx,
		"SELECT count(*) FROM hidden WHERE id='cache-kill'",
	).Scan(&backupCount); err != nil {
		t.Fatal(err)
	}
	if backupCount != 1 {
		t.Fatalf("backup cache-kill rows = %d, want 1", backupCount)
	}

	// An unkill followed by a reopen must not re-adopt the backup row.
	if err := first.Unkill(ctx, "cache-kill"); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second := openTestStore(t)
	t.Cleanup(func() { _ = second.Close() })
	killed, err = second.KilledChats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(killed) != 1 || killed[0].ID != "shared-kill" || killed[0].KilledAt != 3000 {
		t.Fatalf("kills after reopen = %#v, want the original shared kill only", killed)
	}
}

func lookSQLite3(t *testing.T) string {
	t.Helper()

	path, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 CLI is absent: the external-writer seam cannot be exercised")
	}
	return path
}

func runSQLite3(t *testing.T, sqlite3, database, statement string) string {
	t.Helper()

	// Use the .timeout dot command so no PRAGMA result row precedes the query.
	command := exec.Command(
		sqlite3,
		"-batch",
		"-noheader",
		"-cmd",
		".timeout 5000",
		database,
		statement,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("sqlite3 %q: %v: %s", statement, err, output)
	}
	return string(output)
}
