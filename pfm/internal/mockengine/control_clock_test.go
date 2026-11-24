package mockengine

import (
	"context"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/testwait"
)

// TestMockClockNextStepsToTheEarliestArmedWait: next moves to the earliest wait armed on the mock's clock,
// fires it alone, and waits for one to be armed when none is; a closed reader ends it unmoved.
func TestMockClockNextStepsToTheEarliestArmedWait(t *testing.T) {
	clk := newMockClock()
	start := clk.Now()
	late, early := clk.NewTimer(time.Second), clk.NewTimer(300*time.Millisecond)
	if due, ok := clk.next(nil); !ok || due != 300*time.Millisecond {
		t.Fatalf("next = %s, %t, want 300ms to the earlier timer", due, ok)
	}
	if _, ok := testwait.Receive(t, "the earlier timer to fire", early.C()); !ok {
		t.Fatal("the earlier timer's channel closed")
	}
	if got := clk.Pending(); got != 1 {
		t.Fatalf("pending after next = %d, want the later timer alone", got)
	}
	late.Stop()
	stepped := make(chan time.Duration, 1)
	go func() {
		due, _ := clk.next(nil)
		stepped <- due
	}()
	clk.NewTimer(2 * time.Second)
	if due, _ := testwait.Receive(t, "next to step to the timer armed after it began", stepped); due != 2*time.Second {
		t.Fatalf("next = %s, want 2s to the timer armed while it waited", due)
	}
	done := make(chan struct{})
	close(done)
	if due, ok := clk.next(done); ok || due != 0 {
		t.Fatalf("next with its reader closed = %s, %t, want 0s, false", due, ok)
	}
	if moved := clk.Now().Sub(start); moved != 2300*time.Millisecond {
		t.Fatalf("the clock moved %s in all, want 2.3s", moved)
	}
}

// TestClockBoundEndsOnlyOnceTheBoundPasses: a clockBound context ends at its bound on its clock and names
// that cause; one its caller or parent ends first stops its timer and never reads as the bound passing.
func TestClockBoundEndsOnlyOnceTheBoundPasses(t *testing.T) {
	for _, row := range []struct {
		name        string
		end         func(clk *mockClock, cancel, cancelParent func())
		boundPassed bool
	}{
		{"the bound passes", func(clk *mockClock, _, _ func()) { clk.Advance(5 * time.Second) }, true},
		{"its caller cancels", func(_ *mockClock, cancel, _ func()) { cancel() }, false},
		{"its parent ends", func(_ *mockClock, _, cancelParent func()) { cancelParent() }, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			clk := newMockClock()
			parent, cancelParent := context.WithCancel(context.Background())
			defer cancelParent()
			bounded, cancel := clockBound(parent, clk, 5*time.Second)
			defer cancel()
			clk.Advance(5*time.Second - time.Nanosecond)
			if err := bounded.Err(); err != nil {
				t.Fatalf("ended %v a nanosecond before its bound, want it alive", err)
			}
			row.end(clk, cancel, cancelParent)
			testwait.Until(t, "the bounded context to end", func() (bool, string) {
				return bounded.Err() != nil, "alive"
			})
			if got := boundPassed(bounded); got != row.boundPassed {
				t.Fatalf("boundPassed = %t (cause %v), want %t", got, context.Cause(bounded), row.boundPassed)
			}
		})
	}
}
