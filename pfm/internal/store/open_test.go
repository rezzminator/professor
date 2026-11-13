package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

// TestOpenMemoryMatchesOpen pins the unit-test door: OpenMemory builds the
// cache Open builds, object for object, at the same schema version, keeps both
// halves in memory and every store apart, and writes a kill through its shared
// half.
func TestOpenMemoryMatchesOpen(t *testing.T) {
	setStoreTestJail(t)
	ctx := context.Background()
	file := openFileTestStore(t)
	t.Cleanup(func() { _ = file.Close() })
	memory, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = memory.Close() })
	if got, want := schemaObjects(t, memory), schemaObjects(t, file); !slices.Equal(got, want) {
		t.Fatalf("in-memory cache schema =\n%s\nwant Open's\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	assertSchemaVersion(t, memory, SchemaVersion)
	if err := memory.SharedDegraded(); err != nil {
		t.Fatalf("SharedDegraded() = %v", err)
	}
	for _, path := range []string{memory.Path(), memory.SharedPath()} {
		if !sqlitedb.InMemory(path) {
			t.Fatalf("database %q is not in memory", path)
		}
	}
	if memory.Path() == memory.SharedPath() {
		t.Fatalf("cache and shared state share the database %s", memory.Path())
	}
	if err := memory.Kill(ctx, Killed{ID: "memory-kill", KilledAt: 42}); err != nil {
		t.Fatal(err)
	}
	killed, found, err := memory.Killed(ctx, "memory-kill")
	if err != nil || !found || killed.KilledAt != 42 {
		t.Fatalf("Killed() = %#v, %v, %v; want killed at 42", killed, found, err)
	}
	other, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	if _, found, err := other.Killed(ctx, "memory-kill"); err != nil || found {
		t.Fatalf("a second OpenMemory store saw the first's kill: found=%v err=%v", found, err)
	}
}

// TestOpenMemoryReadsNoEnvironmentAndWritesNoFile pins what OpenMemory never
// touches: a config that makes Open fail is never read, and the working
// directory and home stay empty.
func TestOpenMemoryReadsNoEnvironmentAndWritesNoFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	home := filepath.Join(dir, "home")
	config := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(config, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvHome, home)
	t.Setenv(paths.EnvConfig, config)
	t.Setenv(paths.EnvStateDB, "")
	t.Setenv(paths.EnvCacheDB, "")
	if opened, err := Open(); err == nil {
		_ = opened.Close()
		t.Fatal("Open() with a broken config succeeded; the fixture does not break the environment")
	}
	memory, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory() = %v, want no environment read", err)
	}
	if err := memory.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "broken.json" {
		t.Fatalf("working directory %s holds %v (%v), want only broken.json", dir, entries, err)
	}
}

// schemaObjects reads a store's cache schema, one "type name table sql" line per
// object in type and name order.
func schemaObjects(t *testing.T, store *Store) []string {
	t.Helper()
	rows, err := store.db.Query(
		"SELECT type||' '||name||' '||tbl_name||' '||COALESCE(sql,'') FROM sqlite_master ORDER BY type, name")
	if err != nil {
		t.Fatal(err)
	}
	var objects []string
	for rows.Next() {
		var object string
		if err := rows.Scan(&object); err != nil {
			t.Fatal(errors.Join(err, rows.Close()))
		}
		objects = append(objects, object)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	return objects
}
