package fleetdb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// TestSeatRunRecipe pins a seat's private launch recipe: a 0600 file under
// seats/ beside the shared database, read back as written and gone once
// deleted; an invalid seat id, and the in-memory shared state, which has no
// directory for it, are refused by every operation.
func TestSeatRunRecipe(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	file := OpenSharedState(ctx, paths.Values{StateDB: filepath.Join(dir, "pfm.db")})
	t.Cleanup(func() { _ = file.Close() })
	memory := OpenSharedStateMemory(ctx)
	t.Cleanup(func() { _ = memory.Close() })
	run := SeatRun{Binary: "/fixture/engine", Args: []string{"--resume"}, Env: []string{"A=1"}, CWD: "/fixture"}
	if err := file.WriteSeatRun("seat-1", run); err != nil {
		t.Fatal(err)
	}
	recipe := filepath.Join(dir, "seats", "seat-1.json")
	if info, err := os.Stat(recipe); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("recipe %s: %v, %v; want a 0600 file", recipe, info, err)
	}
	if got, err := file.ReadSeatRun("seat-1"); err != nil || !reflect.DeepEqual(got, run) {
		t.Fatalf("ReadSeatRun() = %#v, %v; want %#v", got, err, run)
	}
	if err := file.DeleteSeatRun("seat-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(recipe); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted recipe %s: %v, want gone", recipe, err)
	}
	for _, tc := range []struct {
		name  string
		state *Store
		id    string
		want  string
	}{
		{name: "invalid seat id", state: file, id: "../seat", want: `seat run: invalid seat id "../seat"`},
		{
			name: "in-memory shared state", state: memory, id: "seat-1",
			want: `seat run "seat-1": the in-memory shared state has no directory for its recipe`,
		},
	} {
		if err := tc.state.WriteSeatRun(tc.id, run); err == nil || err.Error() != tc.want {
			t.Errorf("%s: WriteSeatRun() = %v, want %q", tc.name, err, tc.want)
		}
		if _, err := tc.state.ReadSeatRun(tc.id); err == nil || err.Error() != tc.want {
			t.Errorf("%s: ReadSeatRun() = %v, want %q", tc.name, err, tc.want)
		}
		if err := tc.state.DeleteSeatRun(tc.id); err == nil || err.Error() != tc.want {
			t.Errorf("%s: DeleteSeatRun() = %v, want %q", tc.name, err, tc.want)
		}
	}
}
