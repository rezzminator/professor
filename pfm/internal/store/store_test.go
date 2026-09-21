package store

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestOpenMigratesAndReopensIdempotently(t *testing.T) {
	dbPath := setStoreTestJail(t)
	ctx := context.Background()

	first := openTestStore(t)
	if got := first.Path(); got != dbPath {
		t.Fatalf("Path() = %q, want %q", got, dbPath)
	}
	assertSchemaVersion(t, first, SchemaVersion)
	assertTables(t, first, []string{
		"chat_summaries",
		"cx_names",
		"epic_injections",
		"hidden",
		"meta",
		"oc_sessions",
		"rollouts",
		"transcripts",
	})
	assertPragmas(t, first)
	if got := first.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("MaxOpenConnections = %d, want 1", got)
	}
	if err := first.SetMeta(ctx, "migration_sentinel", "preserved"); err != nil {
		t.Fatalf("SetMeta() error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}

	second := openTestStore(t)
	t.Cleanup(func() { _ = second.Close() })
	assertSchemaVersion(t, second, SchemaVersion)
	assertPragmas(t, second)
	value, found, err := second.Meta(ctx, "migration_sentinel")
	if err != nil {
		t.Fatalf("Meta() error = %v", err)
	}
	if !found || value != "preserved" {
		t.Fatalf("Meta() = %q, %v, want %q, true", value, found, "preserved")
	}
}

func setStoreTestJail(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	dbPath := filepath.Join(root, "state", "fleet.db")
	t.Setenv("TMUX_TMPDIR", filepath.Join(root, "t"))
	t.Setenv(paths.EnvDB, dbPath)
	// Pinned explicitly, not merely inherited from the jailed home: nothing in
	// this package may reach the real ~/.cc/fleet.db, which the live fleet is
	// writing while these tests run.
	t.Setenv(paths.EnvFleetDB, filepath.Join(root, "cc", "fleet.db"))
	t.Setenv(paths.EnvSIDDir, filepath.Join(root, "sid"))
	t.Setenv(paths.EnvClaudeRoots, filepath.Join(root, "claude"))
	t.Setenv(paths.EnvCodexHome, filepath.Join(root, "codex"))
	t.Setenv(paths.EnvTmuxDir, filepath.Join(root, "tmux"))
	t.Setenv(paths.EnvHome, filepath.Join(root, "home"))
	return dbPath
}

func openTestStore(t *testing.T, options ...OpenOption) *Store {
	t.Helper()

	store, err := Open(options...)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	return store
}

func assertSchemaVersion(t *testing.T, store *Store, want int) {
	t.Helper()

	got, err := store.UserVersion(context.Background())
	if err != nil {
		t.Fatalf("UserVersion() error = %v", err)
	}
	if got != want {
		t.Fatalf("UserVersion() = %d, want %d", got, want)
	}
}

func assertTables(t *testing.T, store *Store, want []string) {
	t.Helper()

	rows, err := store.db.Query(`
SELECT name
FROM sqlite_master
WHERE type='table' AND name NOT LIKE 'sqlite_%'
ORDER BY name`)
	if err != nil {
		t.Fatalf("query schema tables: %v", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("close rows: %v", err)
		}
	}()

	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan schema table: %v", err)
		}
		got = append(got, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate schema tables: %v", err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("schema tables = %q, want %q", got, want)
	}
}

func assertPragmas(t *testing.T, store *Store) {
	t.Helper()

	var journalMode string
	if err := store.db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}

	var synchronous, busyTimeout, foreignKeys int
	if err := store.db.QueryRow("PRAGMA synchronous").Scan(&synchronous); err != nil {
		t.Fatalf("query synchronous: %v", err)
	}
	if synchronous != 1 {
		t.Fatalf("synchronous = %d, want 1 (NORMAL)", synchronous)
	}
	if err := store.db.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatalf("query busy_timeout: %v", err)
	}
	if busyTimeout != 10000 {
		t.Fatalf("busy_timeout = %d, want 10000", busyTimeout)
	}
	if err := store.db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("query foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d, want 1", foreignKeys)
	}
}
