package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
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
		"meta",
		"oc_sessions",
		"rollouts",
		"transcripts",
	})
	if _, err := os.Stat(dbPath + ".bak-before-v1"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fresh cache backup=%v", err)
	}
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

func TestCacheV9AdoptsBeforeDroppingHiddenAndBacksUp(t *testing.T) {
	path := setStoreTestJail(t)
	ctx := context.Background()
	db, err := sqlitedb.OpenStore(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	for version := 1; version <= 8; version++ {
		if _, err := db.ExecContext(ctx, migrations[version-1]); err != nil {
			t.Fatalf("v%d: %v", version, err)
		}
		if _, err := db.ExecContext(ctx, "PRAGMA user_version="+string(rune('0'+version))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO hidden(id,engine,hidden_at) VALUES('adopt-me','cc',77)"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := OpenContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	}()
	assertSchemaVersion(t, s, 9)
	assertTables(
		t,
		s,
		[]string{"chat_summaries", "cx_names", "epic_injections", "meta", "oc_sessions", "rollouts", "transcripts"},
	)
	if got, found, err := s.Meta(ctx, adoptedKillsMeta); err != nil || !found || got != "1" {
		t.Fatalf("adoption flag=%q,%v,%v", got, found, err)
	}
	killed, err := s.state.KilledAt(ctx)
	if err != nil || killed["adopt-me"] != 77 {
		t.Fatalf("adopted kill=%v,%v", killed, err)
	}
	backup, err := sql.Open("sqlite", path+".bak-before-v9")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := backup.Close(); err != nil {
			t.Error(err)
		}
	}()
	var count int
	if err := backup.QueryRow("SELECT count(*) FROM hidden WHERE id='adopt-me'").
		Scan(&count); err != nil ||
		count != 1 {
		t.Fatalf("backup hidden=%d,%v", count, err)
	}
}

// TestConcurrentOpensUpgradeOnce is the post-upgrade stampede: the statusline,
// the async callmeter hooks and the picker all open a v7 cache at once. Each
// must see the version the previous migrator left, never re-apply v8's ALTER
// TABLE or adopt from a hidden table another opener already dropped.
func TestConcurrentOpensUpgradeOnce(t *testing.T) {
	for round := 0; round < 6; round++ {
		path := setStoreTestJail(t)
		ctx := context.Background()
		db, err := sqlitedb.OpenStore(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		for version := 1; version <= 7; version++ {
			if _, err := db.ExecContext(ctx, migrations[version-1]); err != nil {
				t.Fatalf("v%d: %v", version, err)
			}
			if _, err := db.ExecContext(ctx, "PRAGMA user_version="+string(rune('0'+version))); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := db.ExecContext(ctx, "INSERT INTO hidden(id,engine,hidden_at) VALUES('race','cc',9)"); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		// The shared pfm.db already exists after an upgrade; its first-creation
		// race is sqlitedb's, not this migration's.
		resolved, err := paths.Resolve()
		if err != nil {
			t.Fatal(err)
		}
		if err := fleetdb.OpenSharedState(ctx, resolved).Close(); err != nil {
			t.Fatal(err)
		}
		const openers = 8
		start := make(chan struct{})
		errs := make(chan error, openers)
		for range openers {
			go func() {
				<-start
				s, err := OpenContext(ctx)
				if err == nil {
					err = s.Close()
				}
				errs <- err
			}()
		}
		close(start)
		for range openers {
			if err := <-errs; err != nil {
				t.Fatalf("round %d: concurrent open: %v", round, err)
			}
		}
		s, err := OpenContext(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertSchemaVersion(t, s, 9)
		killed, err := s.state.KilledAt(ctx)
		if closeErr := s.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		if err != nil || killed["race"] != 9 {
			t.Fatalf("round %d: adopted kill=%v,%v", round, killed, err)
		}
	}
}

func TestCacheV9AdoptionFailureLeavesV8(t *testing.T) {
	path := setStoreTestJail(t)
	ctx := context.Background()
	db, err := sqlitedb.OpenStore(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	for version := 1; version <= 8; version++ {
		if _, err := db.ExecContext(ctx, migrations[version-1]); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, "PRAGMA user_version="+string(rune('0'+version))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO hidden(id,engine,hidden_at) VALUES('keep','cc',77)"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvStateDB, filepath.Join(blocker, "pfm.db"))
	if _, err := OpenContext(ctx); err == nil || !strings.Contains(err.Error(), "shared") {
		t.Fatalf("OpenContext error=%v", err)
	}
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
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 8 {
		t.Fatalf("after failed adoption version=%d,%v", version, err)
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM hidden WHERE id='keep'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("legacy kill=%d,%v", count, err)
	}
}

func setStoreTestJail(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	dbPath := filepath.Join(root, "state", "pfm-cache.db")
	t.Setenv("TMUX_TMPDIR", filepath.Join(root, "t"))
	t.Setenv(paths.EnvCacheDB, dbPath)
	// Pinned explicitly, not merely inherited from the jailed home: nothing in
	// this package may reach the real ~/.local/state/pfm/pfm.db, which the live fleet is
	// writing while these tests run.
	t.Setenv(paths.EnvStateDB, filepath.Join(root, "cc", "pfm.db"))
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

// plantLegacy writes a legacy database file at path.
func plantLegacy(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertStoreLegacyPending(t *testing.T, err error, target, legacy string) {
	t.Helper()
	if !errors.Is(err, paths.ErrLegacyPending) {
		t.Fatalf("OpenContext error = %v, want paths.ErrLegacyPending", err)
	}
	for _, want := range []string{target, legacy, "run pfm install"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q lacks %q", err, want)
		}
	}
}

func TestOpenRefusesCacheCreateWhileLegacyCacheWaits(t *testing.T) {
	cacheDB := setStoreTestJail(t)
	home := os.Getenv(paths.EnvHome)
	legacy := paths.LegacyCacheDB(home)
	plantLegacy(t, legacy)
	for name, open := range map[string]func() (*Store, error){
		"Open":        func() (*Store, error) { return Open() },
		"OpenContext": func() (*Store, error) { return OpenContext(context.Background()) },
	} {
		store, err := open()
		if store != nil {
			_ = store.Close()
			t.Fatalf("%s opened a store beside the legacy cache", name)
		}
		assertStoreLegacyPending(t, err, cacheDB, legacy)
	}
	if _, err := os.Lstat(cacheDB); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cache database created (stat err %v)", err)
	}
}

func TestOpenRefusesStateCreateBeforeOpeningEither(t *testing.T) {
	cacheDB := setStoreTestJail(t)
	home := os.Getenv(paths.EnvHome)
	stateDB := os.Getenv(paths.EnvStateDB)
	plantLegacy(t, cacheDB) // the cache target exists
	cacheBefore, err := os.ReadFile(cacheDB)
	if err != nil {
		t.Fatal(err)
	}
	legacy := paths.LegacyStateDB(home)
	plantLegacy(t, legacy)
	_, err = OpenContext(context.Background())
	assertStoreLegacyPending(t, err, stateDB, legacy)
	if _, statErr := os.Lstat(filepath.Dir(stateDB)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("state directory created (stat err %v)", statErr)
	}
	cacheAfter, err := os.ReadFile(cacheDB)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(cacheAfter, cacheBefore) {
		t.Fatal("cache database was opened and rewritten before the state refusal")
	}
}

func TestOpenCreatesBesideLegacyWhenTargetsExistOrHomeIsFresh(t *testing.T) {
	cacheDB := setStoreTestJail(t)
	home := os.Getenv(paths.EnvHome)
	// Fresh home: both created.
	openTestStore(t)
	// Targets exist: legacy files planted afterwards change nothing.
	plantLegacy(t, paths.LegacyCacheDB(home))
	plantLegacy(t, paths.LegacyStateDB(home))
	openTestStore(t)
	if _, err := os.Stat(cacheDB); err != nil {
		t.Fatal(err)
	}
}

func TestOpenLegacyCacheUnreadableIsAnError(t *testing.T) {
	setStoreTestJail(t)
	home := os.Getenv(paths.EnvHome)
	// {home}/.local/state/pfm as a regular file: the legacy stat fails with ENOTDIR.
	plantLegacy(t, filepath.Join(home, ".local", "state", "pfm"))
	_, err := OpenContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), paths.LegacyCacheDB(home)) {
		t.Fatalf("OpenContext error = %v, want one naming %s", err, paths.LegacyCacheDB(home))
	}
}
