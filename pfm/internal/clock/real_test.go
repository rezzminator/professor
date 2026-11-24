package clock

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The real clock's tests call realClock{} itself, never Real, so no test swap of Real changes what they
// prove, and each proof is a zero-length wait or a cancelled context: what the clock returns, never how
// long it took.

func TestRealNowAdvancesWithTheWallClock(t *testing.T) {
	before := time.Now()
	first := realClock{}.Now()
	after := time.Now()
	if first.Before(before) || first.After(after) {
		t.Fatalf("realClock.Now() = %v, want a wall-clock reading between %v and %v", first, before, after)
	}
	second := realClock{}.Now()
	if later := time.Now(); second.Before(after) || second.After(later) {
		t.Fatalf("realClock.Now() did not follow the wall clock forward: second=%v, want between %v and %v",
			second, after, later)
	}
}

func TestRealSleepReturnsWhenCtxIsCancelledBeforeTheDuration(t *testing.T) {
	for _, d := range []time.Duration{time.Hour, 0, -time.Second} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := (realClock{}).Sleep(ctx, d); !errors.Is(err, context.Canceled) {
			t.Errorf("realClock.Sleep(cancelled ctx, %v) = %v, want context.Canceled", d, err)
		}
	}
}

func TestRealSleepReturnsAfterItsDurationOnAnUncancelledCtx(t *testing.T) {
	for _, d := range []time.Duration{0, -time.Second, time.Nanosecond} {
		if err := (realClock{}).Sleep(context.Background(), d); err != nil {
			t.Errorf("realClock.Sleep(uncancelled ctx, %v) = %v, want nil", d, err)
		}
	}
}

func TestRealTimerFiresOnItsOwnChannel(t *testing.T) {
	before := time.Now()
	timer := realClock{}.NewTimer(0)
	if fired := <-timer.C(); fired.Before(before) {
		t.Fatalf("zero-length real timer fired at %v, before it was made at %v", fired, before)
	}
	if timer.Stop() {
		t.Fatal("Stop() on a fired real timer = true, want false")
	}
	if timer.Reset(0) {
		t.Fatal("Reset(0) on a fired, stopped real timer = true, want false")
	}
	<-timer.C()
}

func TestRealTickerFiresRepeatedly(t *testing.T) {
	before := time.Now()
	ticker := realClock{}.NewTicker(time.Nanosecond)
	defer ticker.Stop()
	// A tick carries the time it was due, read by a sender the scheduler can pause mid-stamp, so two
	// ticks prove nothing about each other's order: each is held only to the ticker's own start.
	for _, tick := range []string{"first", "second"} {
		if fired := <-ticker.C(); fired.Before(before) {
			t.Fatalf("real ticker's %s tick fired at %v, before the ticker was made at %v", tick, fired, before)
		}
	}
}
