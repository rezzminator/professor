package fleetdb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

// stageSharedStateVersion creates a current state file, records one kill and
// sets its user_version, so a read-only open meets the given schema.
func stageSharedStateVersion(t *testing.T, path string, version int) {
	t.Helper()
	ctx := context.Background()
	state := OpenSharedState(ctx, paths.Values{StateDB: path})
	if err := state.Degraded(); err != nil {
		t.Fatal(err)
	}
	if err := state.Kill(ctx, "kept-kill", 7); err != nil {
		t.Fatal(errors.Join(err, state.Close()))
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sqlitedb.OpenReadWrite(path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version=%d", version)); err != nil {
		t.Fatal(errors.Join(err, db.Close()))
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenSharedStateReadOnlyNeverCreatesOrMigrates(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		version int // 0: no file
		empty   bool
		want    func(error) bool
	}{
		{name: "absent", want: func(err error) bool { return errors.Is(err, ErrAbsent) }},
		{name: "empty", empty: true, want: func(err error) bool { return errors.Is(err, ErrAbsent) }},
		{name: "older", version: SchemaVersion - 1, want: func(err error) bool {
			var mismatch *SchemaMismatchError
			return errors.As(err, &mismatch) && mismatch.Found == SchemaVersion-1 && !mismatch.Newer()
		}},
		{name: "newer", version: SchemaVersion + 1, want: func(err error) bool {
			var mismatch *SchemaMismatchError
			return errors.As(err, &mismatch) && mismatch.Found == SchemaVersion+1 && mismatch.Newer()
		}},
		{name: "current", version: SchemaVersion, want: func(err error) bool { return err == nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "state", "pfm.db")
			switch {
			case test.empty:
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			case test.version != 0:
				stageSharedStateVersion(t, path, test.version)
			}
			before, _ := os.ReadFile(path)
			state := OpenSharedStateReadOnly(context.Background(), paths.Values{StateDB: path})
			degraded := state.Degraded()
			if !test.want(degraded) {
				t.Fatalf("degraded=%v", degraded)
			}
			records, err := state.KilledRecords(context.Background())
			switch {
			case test.version == SchemaVersion:
				if err != nil || records["kept-kill"].KilledAt != 7 {
					t.Fatalf("current read-only kills=%v err=%v", records, err)
				}
			case degraded != nil && errors.Is(degraded, ErrAbsent):
				if err != nil || len(records) != 0 {
					t.Fatalf("absent kills=%v err=%v, want none", records, err)
				}
			default:
				var mismatch *SchemaMismatchError
				if !errors.As(err, &mismatch) {
					t.Fatalf("mismatched kills=%v err=%v, want the could-not-look error", records, err)
				}
			}
			if err := state.Close(); err != nil {
				t.Fatal(err)
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatalf("read-only open changed %s", path)
			}
			if test.version == 0 && !test.empty {
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("read-only open created %s (stat err=%v)", path, err)
				}
			}
			backups, err := filepath.Glob(path + ".bak-before-v*")
			if err != nil || len(backups) != 0 {
				t.Fatalf("read-only open wrote backups %v (err=%v)", backups, err)
			}
		})
	}
}
