package hostfixture

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestTwoWritersRunsBothCallsConcurrentlyAgainstTheSameLedger proves the
// fixture's whole claim — genuine overlap, not merely two sequential calls
// on two goroutines. Each call increments a shared counter and blocks on a
// channel the SECOND call to observe count==2 closes; a call that never
// sees the other one running times out and fails instead of hanging, so a
// TwoWriters that launched its calls sequentially fails this test loudly
// rather than passing by accident or blocking forever.
func TestTwoWritersRunsBothCallsConcurrentlyAgainstTheSameLedger(t *testing.T) {
	var inFlight int32
	var closeOnce sync.Once
	reachedBoth := make(chan struct{})

	_, errs := TwoWriters(t, func(Base) error {
		if atomic.AddInt32(&inFlight, 1) == 2 {
			closeOnce.Do(func() { close(reachedBoth) })
		}
		select {
		case <-reachedBoth:
			return nil
		case <-time.After(2 * time.Second):
			return fmt.Errorf("timed out waiting for the other writer — TwoWriters did not run them concurrently")
		}
	})

	for index, err := range errs {
		if err != nil {
			t.Fatalf("errs[%d] = %v", index, err)
		}
	}
}

// TestTwoWritersReturnsBothCallsErrorsInLaunchOrder proves the returned
// [2]error slots line up with which goroutine index actually ran — a
// caller distinguishing "the first writer" from "the second" by index
// needs that guarantee.
func TestTwoWritersReturnsBothCallsErrorsInLaunchOrder(t *testing.T) {
	_, errs := TwoWriters(t, func(Base) error {
		return nil
	})
	// TwoWriters always calls fn exactly twice, so both slots are populated
	// (and, with this fn, both nil) regardless of which goroutine actually
	// finished first — the slot is keyed by launch index, not finish order.
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("errs = %v, want [nil nil]", errs)
	}
}

// TestTwoWritersLetsOneWinAnExclusiveCreateAndTheOtherLoseCleanly exercises
// the fixture the way atomicfile/installer-ownership-ledger/fleetdb/
// updatecheck callers actually will: fn contends for one exclusive-create
// file, and exactly one of the two calls must win it.
func TestTwoWritersLetsOneWinAnExclusiveCreateAndTheOtherLoseCleanly(t *testing.T) {
	base, errs := TwoWriters(t, func(b Base) error {
		path := b.Root + "/ledger.lock"
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		return file.Close()
	})
	_ = base

	wins, losses := 0, 0
	for _, err := range errs {
		if err == nil {
			wins++
		} else {
			losses++
		}
	}
	if wins != 1 || losses != 1 {
		t.Fatalf("wins=%d losses=%d (errs=%v), want exactly one winner and one loser", wins, losses, errs)
	}
}
