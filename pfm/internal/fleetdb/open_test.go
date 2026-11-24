package fleetdb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
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

// TestSharedOpenBringsASchemaOncePerProcess: a second open of a database this
// process already brought to schema reads its fingerprint and runs no schema
// step; a schema or version changed between the two opens gets every step
// again. Serial: obs.Test.
func TestSharedOpenBringsASchemaOncePerProcess(t *testing.T) {
	const fingerprint, schema, repair = "select pragma_user_version", "create meta", "select seat"
	for _, test := range []struct {
		name         string
		between      string
		statements   []string
		wantDegraded string
	}{
		{
			name:       "an untouched database runs no schema step",
			statements: []string{fingerprint, repair},
		},
		{
			name:       "a dropped column is added again",
			between:    "ALTER TABLE hidden DROP COLUMN revision",
			statements: []string{fingerprint, schema, fingerprint, repair},
		},
		{
			name:       "a newer version is refused",
			between:    fmt.Sprintf("PRAGMA user_version=%d", SchemaVersion+1),
			statements: []string{fingerprint, schema},
			wantDegraded: fmt.Sprintf(
				"migrate shared state database: shared database %%s schema version %d is newer than binary version %d",
				SchemaVersion+1, SchemaVersion,
			),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, recorder := obs.Test(t)
			path := filepath.Join(t.TempDir(), "pfm.db")
			values := paths.Values{StateDB: path}
			first := OpenSharedState(ctx, values)
			if err := errors.Join(first.Degraded(), first.Close()); err != nil {
				t.Fatal(err)
			}
			if test.between != "" {
				seedSharedState(t, path, test.between)
			}
			before := len(recorder.Records())
			state := OpenSharedState(ctx, values)
			defer func() { _ = state.Close() }()
			var statements []string
			for _, record := range recorder.Records()[before:] {
				if record.Message == "db.statement" {
					op, _ := record.Field("op")
					table, _ := record.Field("table")
					statements = append(statements, fmt.Sprint(op, " ", table))
				}
			}
			if !reflect.DeepEqual(statements, test.statements) {
				t.Errorf("second open of %s ran %q, want %q", path, statements, test.statements)
			}
			if test.wantDegraded != "" {
				want := fmt.Sprintf(test.wantDegraded, path)
				if err := state.Degraded(); err == nil || err.Error() != want {
					t.Fatalf("second open of %s degraded = %v, want %s", path, err, want)
				}
				return
			}
			if err := state.Degraded(); err != nil {
				t.Fatalf("second open of %s degraded: %v", path, err)
			}
			if columns := queryColumn(t, state, "SELECT name FROM pragma_table_info('hidden')"); !reflect.DeepEqual(
				columns, []string{"uuid", "hidden_at", "at_payload", "revision"},
			) {
				t.Fatalf("hidden columns of %s after the second open = %v", path, columns)
			}
			if err := state.Kill(ctx, "warm-kill", 42); err != nil {
				t.Fatal(err)
			}
			if killed, err := state.KilledAt(ctx); err != nil ||
				!reflect.DeepEqual(killed, map[string]int64{"warm-kill": 42}) {
				t.Fatalf("KilledAt() on %s = %v, %v; want warm-kill at 42", path, killed, err)
			}
		})
	}
}
