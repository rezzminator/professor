package fleetdb

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// TestOpenSharedStateConcurrentlyInFreshHome: the first pfm commands of a fresh machine (two picker panes
// opening at once) create and migrate the shared state database together, and every one of them opens it.
func TestOpenSharedStateConcurrentlyInFreshHome(t *testing.T) {
	t.Parallel()
	const rounds, openers = 20, 8
	for round := range rounds {
		path := filepath.Join(t.TempDir(), "pfm.db")
		errs := make([]error, openers)
		var wg sync.WaitGroup
		for opener := range openers {
			wg.Go(func() {
				state := OpenSharedState(context.Background(), paths.Values{StateDB: path})
				errs[opener] = errors.Join(state.Degraded(), state.Close())
			})
		}
		wg.Wait()
		if err := errors.Join(errs...); err != nil {
			t.Fatalf(
				"round %d: %d concurrent opens of fresh %s: %v; want every open to succeed",
				round,
				openers,
				path,
				err,
			)
		}
	}
}
