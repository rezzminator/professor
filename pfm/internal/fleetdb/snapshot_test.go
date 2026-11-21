package fleetdb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestRestoreSharedStateReturnsTheDatabaseANewerBuildMigrated(t *testing.T) {
	newer := SchemaVersion + 1
	tests := []struct {
		name     string
		before   bool // a database exists when the snapshot is taken
		migrate  bool // the candidate then stamps it newer than this binary
		restored bool
		noCopy   bool // the restore refuses: nothing to return to
		version  int  // the database's user_version afterwards; 0 when there is none
	}{
		{
			name: "a database the candidate migrated past this binary", before: true, migrate: true,
			restored: true, version: SchemaVersion,
		},
		{name: "a database still at this binary's schema", before: true, version: SchemaVersion},
		{
			name: "a database the candidate created with no copy before it", migrate: true, noCopy: true,
			version: newer,
		},
		{name: "no database"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			testjail.DatabaseFiles(t, dir)
			path, target := filepath.Join(dir, "pfm.db"), filepath.Join(dir, "state.db")
			kept := fmt.Sprintf("%s.rolled-back-v%d", path, newer)
			if test.before {
				snapshotTestSeed(t, path)
			}
			snapshot, err := SnapshotSharedState(ctx, path, target)
			wantSnapshot := ""
			if test.before {
				wantSnapshot = target
			}
			if err != nil || snapshot != wantSnapshot {
				t.Fatalf("SnapshotSharedState(%s) = %q, %v; want %q, nil", path, snapshot, err, wantSnapshot)
			}
			if test.migrate {
				snapshotTestExec(
					t,
					path,
					"CREATE TABLE candidate_only (v TEXT)",
					fmt.Sprintf("PRAGMA user_version=%d", newer),
				)
			}
			result, err := RestoreSharedState(ctx, path, snapshot)
			want := SharedStateRestore{}
			if test.restored {
				want = SharedStateRestore{Restored: true, From: newer, To: SchemaVersion, Kept: kept}
			}
			wantErr := ""
			if test.noCopy {
				wantErr = fmt.Sprintf(
					"shared database %s is at schema version %d, newer than this binary's %d, "+
						"and has no copy from before the update: move it aside and pfm creates a new one",
					path, newer, SchemaVersion,
				)
			}
			if gotErr := errorText(err); result != want || gotErr != wantErr {
				t.Fatalf("RestoreSharedState(%s) = %+v, %q; want %+v, %q", path, result, gotErr, want, wantErr)
			}
			if test.version == 0 {
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("stat %s = %v, want no database created", path, err)
				}
				return
			}
			if got := snapshotTestVersion(t, path); got != test.version {
				t.Fatalf("%s user_version = %d, want %d", path, got, test.version)
			}
			if !test.restored {
				if _, err := os.Stat(kept); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("stat %s = %v, want no kept copy", kept, err)
				}
				return
			}
			if got := snapshotTestVersion(t, kept); got != newer {
				t.Fatalf("kept %s user_version = %d, want %d", kept, got, newer)
			}
			state := OpenSharedState(ctx, paths.Values{StateDB: path})
			defer func() {
				if err := state.Close(); err != nil {
					t.Errorf("close %s: %v", path, err)
				}
			}()
			if err := state.Degraded(); err != nil {
				t.Fatalf("OpenSharedState(%s) after restore degraded: %v", path, err)
			}
		})
	}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// snapshotTestSeed creates the database at path as this binary does, at SchemaVersion.
func snapshotTestSeed(t *testing.T, path string) {
	t.Helper()
	state := OpenSharedState(context.Background(), paths.Values{StateDB: path})
	if err := errors.Join(state.Degraded(), state.Close()); err != nil {
		t.Fatalf("OpenSharedState(%s): %v", path, err)
	}
}

// snapshotTestExec runs statements on the database at path, creating it bare.
func snapshotTestExec(t *testing.T, path string, statements ...string) {
	t.Helper()
	ctx := context.Background()
	database, err := sqlitedb.OpenStore(ctx, path)
	if err != nil {
		t.Fatalf("OpenStore(%s) = %v", path, err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close %s: %v", path, err)
		}
	}()
	for _, statement := range statements {
		if _, err := database.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %s: %v", path, statement, err)
		}
	}
}

func snapshotTestVersion(t *testing.T, path string) int {
	t.Helper()
	database, err := sqlitedb.OpenStore(context.Background(), path)
	if err != nil {
		t.Fatalf("OpenStore(%s) = %v", path, err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close %s: %v", path, err)
		}
	}()
	version, err := sharedStateVersion(context.Background(), database, path)
	if err != nil {
		t.Fatal(err)
	}
	return version
}
