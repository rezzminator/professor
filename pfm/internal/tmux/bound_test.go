package tmux

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

// TestBoundedWithinTimesTheBoundOnTheCarriedClock: a context carrying a clock
// (WithBoundClock) is bounded on it, ending exactly at the bound and not one
// step before; with no clock, or a nil one, the bound is the standard
// library's own timeout, armed and not yet ended.
func TestBoundedWithinTimesTheBoundOnTheCarriedClock(t *testing.T) {
	fake := clock.NewFake(time.Unix(0, 0))
	bounded, cancel := boundedWithin(WithBoundClock(context.Background(), fake), defaultTimeout)
	defer cancel()
	if pending := fake.Pending(); pending != 1 {
		t.Fatalf("armed bounds on the carried clock = %d (%s), want 1", pending, fake)
	}
	fake.Advance(defaultTimeout - time.Nanosecond)
	if err := bounded.Err(); err != nil {
		t.Fatalf("bound ended %s before %s: %v", time.Nanosecond, defaultTimeout, err)
	}
	fake.Advance(time.Nanosecond)
	if err := bounded.Err(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("bound at %s = %v, want %v", defaultTimeout, err, context.DeadlineExceeded)
	}

	for name, ctx := range map[string]context.Context{
		"no clock":  context.Background(),
		"nil clock": WithBoundClock(context.Background(), nil),
	} {
		bounded, cancelReal := boundedWithin(ctx, defaultTimeout)
		_, hasDeadline := bounded.Deadline()
		err := bounded.Err()
		cancelReal()
		if !hasDeadline || err != nil {
			t.Fatalf("%s: deadline set %v, err %v, want a live context.WithTimeout bound", name, hasDeadline, err)
		}
	}
}
