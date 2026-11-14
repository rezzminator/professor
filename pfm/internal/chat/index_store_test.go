package chat

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
)

func TestOpenFleetIndex(t *testing.T) {
	memory, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := memory.Close(); err != nil {
			t.Errorf("close memory store: %v", err)
		}
	})
	t.Setenv(paths.EnvCacheDB, filepath.Join(t.TempDir(), "pfm-cache.db"))
	t.Setenv(paths.EnvStateDB, filepath.Join(t.TempDir(), "pfm.db"))
	for _, test := range []struct {
		name     string
		injected *store.Store
	}{
		{name: "an injected store is returned and left open on close", injected: memory},
		{name: "no injected store opens the path store and close closes it"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fleetIndex = test.injected
			t.Cleanup(func() { fleetIndex = nil })
			database, closeIndex, err := openFleetIndex(nil)
			if err != nil {
				t.Fatal(err)
			}
			if (database == memory) != (test.injected != nil) {
				t.Fatalf("returned the injected store = %v, want %v", database == memory, test.injected != nil)
			}
			if err := closeIndex(); err != nil {
				t.Fatalf("close: %v", err)
			}
			_, _, err = database.Killed(context.Background(), "absent")
			if (err != nil) != (test.injected == nil) {
				t.Fatalf("store after close: err=%v, want an error only for the store opened here", err)
			}
		})
	}
}
