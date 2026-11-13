package store

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

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
