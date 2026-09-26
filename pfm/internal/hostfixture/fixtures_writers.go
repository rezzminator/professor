package hostfixture

import (
	"sync"
	"testing"
)

// TwoWriters jails a fleet, then launches fn twice concurrently against it
// — the shape a crashed leftover writer or a second concurrent pfm process
// takes against the same ledger, settings file or manifest. Both calls are
// released from a shared barrier so they genuinely race rather than merely
// run in sequence on two goroutines; it returns the jailed Base plus both
// calls' errors in launch order, so a caller can assert "one won, one lost
// cleanly" rather than "both corrupted the file."
func TwoWriters(t *testing.T, fn func(base Base) error) (Base, [2]error) {
	t.Helper()
	base := newBase(t)

	var ready, done sync.WaitGroup
	ready.Add(2)
	done.Add(2)
	start := make(chan struct{})
	var errs [2]error
	for index := range errs {
		go func(index int) {
			defer done.Done()
			ready.Done()
			<-start
			errs[index] = fn(base)
		}(index)
	}
	ready.Wait()
	close(start)
	done.Wait()

	return base, errs
}
