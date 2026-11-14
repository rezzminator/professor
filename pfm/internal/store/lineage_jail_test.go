//go:build jail

package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestV2MigrationCollapsesExistingCodexKillsToLineageRoot(t *testing.T) {
	dbPath := setStoreTestJail(t)
	ctx := context.Background()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, schemaV1); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, "PRAGMA user_version=1"); err != nil {
		t.Fatal(err)
	}
	for _, rollout := range lineageFixtureRollouts() {
		if _, err := database.ExecContext(ctx, `
INSERT INTO rollouts (
  id, path, size, mtime_ns, parsed_offset, cwd, user_thread, session_id,
  parent_thread, first_prompt, prompt_count
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			rollout.ID,
			rollout.Path,
			rollout.Size,
			rollout.MTimeNS,
			rollout.ParsedOffset,
			rollout.CWD,
			boolInteger(rollout.UserThread),
			rollout.SessionID,
			rollout.ParentThread,
			rollout.FirstPrompt,
			rollout.PromptCount,
		); err != nil {
			t.Fatal(err)
		}
	}
	for _, killed := range []Killed{
		{
			ID:              "root",
			Engine:          "cx",
			KilledAt:        100,
			BaselinePrompts: int64Pointer(10),
		},
		{
			ID:              "child-old",
			Engine:          "cx",
			KilledAt:        200,
			BaselinePrompts: int64Pointer(10),
		},
		{
			ID:              "child-newest",
			Engine:          "cx",
			KilledAt:        300,
			BaselinePrompts: int64Pointer(11),
		},
	} {
		if _, err := database.ExecContext(ctx, `
INSERT INTO hidden (id, engine, hidden_at, baseline_prompts)
VALUES (?, ?, ?, ?)`,
			killed.ID,
			killed.Engine,
			killed.KilledAt,
			killed.BaselinePrompts,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	migrated := openFileTestStore(t)
	// The whole chain runs, v1 through the newest: the lineage collapse below
	// has to survive every later migration, not only the one that introduced
	// it.
	assertSchemaVersion(t, migrated, SchemaVersion)
	rollouts, err := migrated.Rollouts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, rollout := range rollouts {
		if rollout.LineageRoot != "root" {
			t.Fatalf("%s lineage_root = %q", rollout.ID, rollout.LineageRoot)
		}
	}
	killed, err := migrated.KilledChats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The merged row keeps the newest hidden_at and drops the retired
	// baseline: the column is NULL for every kill the fleet writes.
	if len(killed) != 1 ||
		killed[0].ID != "root" ||
		killed[0].Engine != "cx" ||
		killed[0].KilledAt != 300 ||
		killed[0].BaselinePrompts != nil {
		t.Fatalf("migrated kills = %#v", killed)
	}
	counts, err := migrated.Counts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts.OrphanedKills != 0 {
		t.Fatalf("migrated orphaned kills = %d", counts.OrphanedKills)
	}
	if err := migrated.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := openFileTestStore(t)
	t.Cleanup(func() { _ = reopened.Close() })
	killed, err = reopened.KilledChats(ctx)
	if err != nil || len(killed) != 1 ||
		killed[0].ID != "root" ||
		killed[0].BaselinePrompts != nil {
		t.Fatalf("idempotent reopen kills = %#v, err=%v", killed, err)
	}
}

// A legacy kill whose time no longer reads survives both one-time migrations:
// the lineage collapse leaves it in place and the shared-store adoption keeps
// it with its time unknown, each warned; the open itself never fails.
func TestLegacyKillWithAnUnreadableTimeSurvivesTheMigrations(t *testing.T) {
	dbPath := setStoreTestJail(t)
	ctx := context.Background()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, schemaV1+`;PRAGMA user_version=1;
INSERT INTO hidden (id, engine, hidden_at) VALUES ('timeless', 'cx', 'noon')`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	var warnings strings.Builder
	migrated := openFileTestStore(t, WithWarningWriter(&warnings))
	t.Cleanup(func() { _ = migrated.Close() })
	assertSchemaVersion(t, migrated, SchemaVersion)
	killed, err := migrated.state.KilledAt(ctx)
	if want := map[string]int64{"timeless": 0}; err != nil || !reflect.DeepEqual(killed, want) {
		t.Fatalf("shared kills = %v, %v; want %v", killed, err, want)
	}
	const scanErr = `converting driver.Value type string ("noon") to a int64: invalid syntax`
	want := "WARNING: pfm left one unreadable Codex kill in " + dbPath + ` uncollapsed: ` +
		`sql: Scan error on column index 2, name "hidden_at": ` + scanErr + "\n" +
		`WARNING: pfm adopted the kill of "timeless" from ` + dbPath + ` with its time unknown: ` +
		`sql: Scan error on column index 1, name "hidden_at": ` + scanErr + "\n"
	if warnings.String() != want {
		t.Fatalf("warnings = %q, want %q", warnings.String(), want)
	}
}

func int64Pointer(value int64) *int64 {
	return &value
}
