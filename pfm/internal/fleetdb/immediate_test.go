package fleetdb

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

// TestOpenSharedStateConcurrentlyInFreshHome: the first pfm commands of a fresh machine (two picker panes
// opening at once) create and migrate the shared state database together, and every one of them opens it.
func TestOpenSharedStateConcurrentlyInFreshHome(t *testing.T) {
	// An opener that finds the migration lock held pauses on the process clock;
	// Drive runs each pause once every opener is parked, so none is waited out.
	fake := clock.NewFake(time.Unix(1_800_000_000, 0))
	testwait.ProcessClock(t, fake)
	const rounds, openers = 20, 8
	for round := range rounds {
		path := filepath.Join(t.TempDir(), "pfm.db")
		errs := make([]error, openers)
		testwait.Drive(t, "concurrent opens of a fresh shared state", fake, func() {
			var wg sync.WaitGroup
			for opener := range openers {
				wg.Go(func() {
					state := OpenSharedState(context.Background(), paths.Values{StateDB: path})
					errs[opener] = errors.Join(state.Degraded(), state.Close())
				})
			}
			wg.Wait()
		})
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
