package clock

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// TestWithTimeoutOnTheRealClockIsTheStandardLibrarysOwn proves the real clock's bound is
// context.WithTimeout itself: the standard library's timer context, ended by its cancel.
func TestWithTimeoutOnTheRealClockIsTheStandardLibrarysOwn(t *testing.T) {
	ctx, cancel := WithTimeout(context.Background(), realClock{}, time.Hour)
	if got := fmt.Sprintf("%T", ctx); got != "*context.timerCtx" {
		t.Errorf("WithTimeout on the real clock = %s, want *context.timerCtx", got)
	}
	cancel()
	if err := ctx.Err(); err != context.Canceled {
		t.Errorf("Err after cancel = %v, want %v", err, context.Canceled)
	}
}

// TestWithTimeoutOnAFakeEndsWhenTheFakeCrossesTheBound walks each end a fake-clock bound meets
// that needs no other goroutine: the timer at d and not a nanosecond before, cancel, a parent
// already ended, a bound already spent.
func TestWithTimeoutOnAFakeEndsWhenTheFakeCrossesTheBound(t *testing.T) {
	why := errors.New("the caller gave up")
	cases := []struct {
		name      string
		parent    func() context.Context
		d         time.Duration
		act       func(fake *Fake, cancel context.CancelFunc)
		before    error // Err after Advance(d - 1ns), before act
		wantErr   error
		wantCause error
	}{
		{
			"the timer fires at d", context.Background, time.Minute,
			func(fake *Fake, _ context.CancelFunc) { fake.Advance(time.Nanosecond) },
			nil, context.DeadlineExceeded, context.DeadlineExceeded,
		},
		{
			"cancel ends it", context.Background, time.Minute,
			func(_ *Fake, cancel context.CancelFunc) { cancel() },
			nil, context.Canceled, context.Canceled,
		},
		{"a parent already ended carries its error and cause", func() context.Context {
			parent, cancel := context.WithCancelCause(context.Background())
			cancel(why)
			return parent
		}, time.Minute, func(*Fake, context.CancelFunc) {}, context.Canceled, context.Canceled, why},
		{
			"a bound already spent",
			context.Background,
			0,
			func(*Fake, context.CancelFunc) {},
			context.DeadlineExceeded,
			context.DeadlineExceeded,
			context.DeadlineExceeded,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := NewFake(epoch)
			ctx, cancel := WithTimeout(tc.parent(), fake, tc.d)
			defer cancel()
			if deadline, ok := ctx.Deadline(); !ok || !deadline.Equal(epoch.Add(tc.d)) {
				t.Errorf("Deadline = (%v, %v), want (%v, true)", deadline, ok, epoch.Add(tc.d))
			}
			fake.Advance(tc.d - time.Nanosecond)
			if err := ctx.Err(); err != tc.before {
				t.Errorf("Err a nanosecond before the bound = %v, want %v", err, tc.before)
			}
			tc.act(fake, cancel)
			select {
			case <-ctx.Done():
			default:
				t.Fatal("Done is open, want closed")
			}
			if err, cause := ctx.Err(), context.Cause(ctx); err != tc.wantErr || cause != tc.wantCause {
				t.Errorf("Err, Cause = %v, %v; want %v, %v", err, cause, tc.wantErr, tc.wantCause)
			}
			child, stop := context.WithCancel(ctx)
			defer stop()
			if err := child.Err(); err != tc.wantErr {
				t.Errorf("a child's Err = %v, want %v", err, tc.wantErr)
			}
			if pending := fake.Pending(); pending != 0 {
				t.Errorf("fake pending = %d, want 0: the bound's timer outlived its end", pending)
			}
		})
	}
}
