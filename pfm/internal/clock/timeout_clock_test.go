package clock_test

// timeout_clock_test.go: clock.WithTimeout's ends that land on a goroutine of their own (a clock
// other than the fake, a parent that ends later), waited for through the wait door, which the
// internal test package cannot import (testwait imports clock).

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

// wrapped is a Clock that is neither the real clock nor a *Fake: the real-wait tripwire's shape.
type wrapped struct{ *clock.Fake }

func TestWithTimeoutEndsOnAnyClocksTimerAndOnItsParent(t *testing.T) {
	epoch := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	why := errors.New("the caller gave up")
	cases := []struct {
		name      string
		end       func(fake *clock.Fake, parentCancel context.CancelCauseFunc)
		wantErr   error
		wantCause error
	}{
		{"the wrapped clock's timer fires", func(fake *clock.Fake, _ context.CancelCauseFunc) {
			fake.Advance(time.Minute)
		}, context.DeadlineExceeded, context.DeadlineExceeded},
		{"the parent ends later", func(_ *clock.Fake, parentCancel context.CancelCauseFunc) {
			parentCancel(why)
		}, context.Canceled, why},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := clock.NewFake(epoch)
			parent, parentCancel := context.WithCancelCause(context.Background())
			defer parentCancel(nil)
			ctx, cancel := clock.WithTimeout(parent, wrapped{fake}, time.Minute)
			defer cancel()
			if deadline, ok := ctx.Deadline(); !ok || !deadline.Equal(epoch.Add(time.Minute)) {
				t.Errorf("Deadline = (%v, %v), want (%v, true)", deadline, ok, epoch.Add(time.Minute))
			}
			fake.Advance(time.Minute - time.Nanosecond)
			if err := ctx.Err(); err != nil {
				t.Errorf("Err a nanosecond before the bound = %v, want nil", err)
			}
			tc.end(fake, parentCancel)
			testwait.Receive(t, "the bound to end", ctx.Done())
			if err, cause := ctx.Err(), context.Cause(ctx); err != tc.wantErr || cause != tc.wantCause {
				t.Errorf("Err, Cause = %v, %v; want %v, %v", err, cause, tc.wantErr, tc.wantCause)
			}
			if pending := fake.Pending(); pending != 0 {
				t.Errorf("fake pending = %d, want 0: the bound's timer outlived its end", pending)
			}
		})
	}
}
