package mockengine

import (
	"context"
	"errors"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

// mockClock is the clock a controlled mock runs on: a clock.Fake only the test moves, which tells the control
// reader each time a wait is armed on it, so a `next` steps to the wait the mock is parked on.
type mockClock struct {
	*clock.Fake
	armed chan struct{}
	// onArm runs at each wait armed, before it is signalled: the mock opens its control FIFO there.
	onArm func()
}

func newMockClock() *mockClock {
	return &mockClock{Fake: clock.NewFake(time.Now()), armed: make(chan struct{}, 1)}
}

func (clk *mockClock) arm() {
	if clk.onArm != nil {
		clk.onArm()
	}
	select {
	case clk.armed <- struct{}{}:
	default:
	}
}

func (clk *mockClock) NewTimer(d time.Duration) clock.Timer {
	timer := clk.Fake.NewTimer(d)
	clk.arm()
	return timer
}

func (clk *mockClock) NewTicker(d time.Duration) clock.Ticker {
	ticker := clk.Fake.NewTicker(d)
	clk.arm()
	return ticker
}

func (clk *mockClock) After(d time.Duration) <-chan time.Time {
	return clk.NewTimer(d).C()
}

// Sleep stops its timer when ctx ends first, so a cancelled sleep is never a wait `next` steps to.
func (clk *mockClock) Sleep(ctx context.Context, d time.Duration) error {
	timer := clk.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// next moves the clock to its earliest armed wait, first waiting until one is armed; false once done closes.
func (clk *mockClock) next(done <-chan struct{}) (time.Duration, bool) {
	for {
		if due, ok := clk.NextDue(); ok {
			due = max(due, 0)
			clk.Advance(due)
			return due, true
		}
		select {
		case <-clk.armed:
		case <-done:
			return 0, false
		}
	}
}

// errBoundPassed is the cause a clockBound context ends with once its clock passes the bound.
var errBoundPassed = errors.New("the bound passed on the mock's clock")

// clockBound is context.WithTimeout on clk: what the real engine enforces (a hook's timeout, a statusline
// run's abort) passes on the mock's clock, so only the test's clock ends it.
func clockBound(ctx context.Context, clk clock.Clock, bound time.Duration) (context.Context, context.CancelFunc) {
	bounded, cancel := context.WithCancelCause(ctx)
	timer := clk.NewTimer(bound)
	go func() {
		select {
		case <-timer.C():
			cancel(errBoundPassed)
		case <-bounded.Done():
		}
	}()
	return bounded, func() {
		timer.Stop()
		cancel(context.Canceled)
	}
}

// boundPassed reports whether a clockBound context ended because its bound passed.
func boundPassed(bounded context.Context) bool {
	return errors.Is(context.Cause(bounded), errBoundPassed)
}
