package store

import (
	"context"
	"os/exec"
	"testing"

	pfmengine "hostops/pfm/internal/engine"
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

// The one-time adoption unions the retired local table into shared SQLite and
// runs once. It never deletes the rollback rows.
func TestAdoptingLocalKillsUnionsOnceAndDeletesNothing(t *testing.T) {
	setStoreTestJail(t)

	first := openTestStore(t)
	ctx := context.Background()
	// A kill in the shape the retired local table held: engine column and all,
	// written straight to the private cache as an older binary would have.
	if _, err := first.db.ExecContext(ctx, `
INSERT INTO hidden(id, engine, hidden_at, baseline_prompts)
VALUES ('cache-kill', 'cc', 4242, 9)`); err != nil {
		t.Fatal(err)
	}
	if err := first.SetMeta(ctx, adoptedKillsMeta, ""); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second := openTestStore(t)
	killed, err := second.KilledChats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(killed) != 1 ||
		killed[0].ID != "cache-kill" || killed[0].KilledAt != 4242 {
		t.Fatalf("adopted kills = %#v", killed)
	}
	// The retired table is left populated: it is the rollback, not a leak.
	var remaining int
	if err := second.db.QueryRowContext(
		ctx,
		"SELECT count(*) FROM hidden",
	).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatalf("retired local killed rows = %d, want the original left in place", remaining)
	}

	// An unkill now, and a reopen must NOT resurrect it: adoption ran once.
	if err := second.Unkill(ctx, "cache-kill"); err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}

	third := openTestStore(t)
	t.Cleanup(func() { _ = third.Close() })
	killed, err = third.KilledChats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(killed) != 0 {
		t.Fatalf("kills after reopen = %#v, want the unkill to have stuck", killed)
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
