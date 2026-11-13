package fleetdb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// TestOpenSharedStateMemoryMatchesTheFile pins the shared half of
// store.OpenMemory: the file opener's schema object for object, an operator
// decision round trip, and the migration lock a no-op that leaves no file.
func TestOpenSharedStateMemoryMatchesTheFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	ctx := context.Background()
	schema := func(state *Store) []string {
		t.Helper()
		if err := state.Degraded(); err != nil {
			t.Fatalf("Degraded() = %v", err)
		}
		rows, err := state.db.QueryContext(ctx,
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
		var version int
		if err := state.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
			t.Fatal(err)
		}
		return append(objects, "user_version "+strconv.Itoa(version))
	}
	file := OpenSharedState(ctx, paths.Values{StateDB: filepath.Join(t.TempDir(), "pfm.db")})
	t.Cleanup(func() { _ = file.Close() })
	memory := OpenSharedStateMemory(ctx)
	t.Cleanup(func() { _ = memory.Close() })
	want, got := schema(file), schema(memory)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf(
			"in-memory shared schema =\n%s\nwant the file's\n%s",
			strings.Join(got, "\n"),
			strings.Join(want, "\n"),
		)
	}
	if want[len(want)-1] != "user_version "+strconv.Itoa(SchemaVersion) {
		t.Fatalf("file schema ends %q, want user_version %d", want[len(want)-1], SchemaVersion)
	}
	if err := memory.Kill(ctx, "memory-kill", 42); err != nil {
		t.Fatal(err)
	}
	if killed, err := memory.KilledAt(
		ctx,
	); err != nil ||
		!reflect.DeepEqual(killed, map[string]int64{"memory-kill": 42}) {
		t.Fatalf("KilledAt() = %v, %v; want memory-kill at 42", killed, err)
	}
	first, err := LockMigration(ctx, memory.Path())
	if err != nil {
		t.Fatal(err)
	}
	held, cancel := context.WithCancel(ctx)
	cancel()
	second, err := LockMigration(held, memory.Path())
	if err != nil {
		t.Fatalf("second LockMigration on memory = %v, want the no-op lock", err)
	}
	if err := errors.Join(second(), first()); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("working directory %s holds %v (%v), want nothing", dir, entries, err)
	}
}
