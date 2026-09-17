package clock

import (
	"context"
	"testing"
	"time"
)

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// TestFakeAdvanceFiresDueWaitersInDueOrder proves the deterministic
// ordering Advance promises: two After channels due at different times both
// fire once Advance crosses the later one, and each carries its OWN due
// time — not the clock's start, not each other's — so a caller can tell
// which fired first from the value alone.
func TestFakeAdvanceFiresDueWaitersInDueOrder(t *testing.T) {
	fake := NewFake(epoch)
	early := fake.After(5 * time.Millisecond)
	late := fake.After(10 * time.Millisecond)

	fake.Advance(10 * time.Millisecond)

	var earlyFired, lateFired time.Time
	select {
	case earlyFired = <-early:
	default:
		t.Fatal("early After did not fire by Advance(10ms)")
	}
	select {
	case lateFired = <-late:
	default:
		t.Fatal("late After did not fire by Advance(10ms)")
	}
	if !earlyFired.Equal(epoch.Add(5 * time.Millisecond)) {
		t.Fatalf("early fired at %v, want %v", earlyFired, epoch.Add(5*time.Millisecond))
	}
	if !lateFired.Equal(epoch.Add(10 * time.Millisecond)) {
		t.Fatalf("late fired at %v, want %v", lateFired, epoch.Add(10*time.Millisecond))
	}
	if !earlyFired.Before(lateFired) {
		t.Fatalf("early (%v) did not fire before late (%v)", earlyFired, lateFired)
	}
}

// TestFakeAdvancePartwayLeavesTheLaterWaiterPending proves Advance only
// fires what is actually due: a waiter past the new time stays registered
// (Pending keeps counting it) and delivers nothing until a further Advance
// reaches it.
func TestFakeAdvancePartwayLeavesTheLaterWaiterPending(t *testing.T) {
	fake := NewFake(epoch)
	early := fake.After(5 * time.Millisecond)
	late := fake.After(50 * time.Millisecond)

	fake.Advance(5 * time.Millisecond)

	select {
	case <-early:
	default:
		t.Fatal("early After did not fire by Advance(5ms)")
	}
	select {
	case v := <-late:
		t.Fatalf("late After fired early with %v, want still pending", v)
	default:
	}
	if got := fake.Pending(); got != 1 {
		t.Fatalf("Pending() = %d, want 1 (the still-due late waiter)", got)
	}

	fake.Advance(45 * time.Millisecond)
	select {
	case <-late:
	default:
		t.Fatal("late After did not fire after the second Advance reached it")
	}
	if got := fake.Pending(); got != 0 {
		t.Fatalf("Pending() = %d, want 0 after both waiters fired", got)
	}
}

// TestFakeTickerCatchesUpWithinOneAdvanceAndStaysRegistered proves a
// repeating ticker reschedules itself across multiple elapsed periods
// inside a single Advance (matching time.Ticker's own lossy, buffer-of-one
// delivery to a slow reader) and remains Pending — a ticker is never a
// one-shot.
func TestFakeTickerCatchesUpWithinOneAdvanceAndStaysRegistered(t *testing.T) {
	fake := NewFake(epoch)
	ticker := fake.NewTicker(5 * time.Millisecond)

	// Three periods elapse (5, 10, 15ms) inside one 17ms Advance; only the
	// first tick survives in the buffer-of-one channel because nothing
	// drains it between periods — the same drop a real time.Ticker gives a
	// receiver that falls behind.
	fake.Advance(17 * time.Millisecond)

	select {
	case got := <-ticker.C():
		if !got.Equal(epoch.Add(5 * time.Millisecond)) {
			t.Fatalf("ticker delivered %v, want the first due tick %v", got, epoch.Add(5*time.Millisecond))
		}
	default:
		t.Fatal("ticker never fired across three elapsed periods")
	}
	if got := fake.Pending(); got != 1 {
		t.Fatalf("Pending() = %d, want 1 (a ticker stays registered after firing)", got)
	}

	ticker.Stop()
	if got := fake.Pending(); got != 0 {
		t.Fatalf("Pending() = %d after Stop(), want 0", got)
	}
}

// TestFakeSleepBlocksUntilAdvanceReleasesIt proves the promised contract: a
// goroutine calling Sleep on a Fake blocks until another goroutine calls
// Advance past its duration.
func TestFakeSleepBlocksUntilAdvanceReleasesIt(t *testing.T) {
	fake := NewFake(epoch)
	done := make(chan error, 1)
	go func() {
		done <- fake.Sleep(context.Background(), 20*time.Millisecond)
	}()

	select {
	case err := <-done:
		t.Fatalf("Sleep returned (%v) before any Advance call", err)
	case <-time.After(20 * time.Millisecond):
		// Real wall-clock time passing must not release a Fake sleep —
		// this is the assertion, not a flake: the goroutine above must
		// still be blocked here.
	}

	fake.Advance(20 * time.Millisecond)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Sleep() error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Sleep did not return within 1s of the releasing Advance")
	}
}

// TestFakeSleepReturnsCtxErrOnCancellationWithoutAnyAdvance proves the
// other half of Sleep's contract: a caller need not wait for Advance at
// all when its context is cancelled first.
func TestFakeSleepReturnsCtxErrOnCancellationWithoutAnyAdvance(t *testing.T) {
	fake := NewFake(epoch)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- fake.Sleep(ctx, time.Hour)
	}()
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("Sleep() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Sleep did not return within 1s of ctx cancellation")
	}
	// The waiter Sleep registered through After is still pending: Advance
	// never ran, and cancellation does not retroactively deregister it —
	// exactly what a caller inspecting Pending() must be able to rely on.
	if got := fake.Pending(); got != 1 {
		t.Fatalf("Pending() = %d, want 1 (the abandoned After waiter)", got)
	}
}

// TestFakeTimerStopPreventsAFutureFire proves Stop removes a timer from
// Advance's consideration — the same "did not fire" contract time.Timer.
// Stop gives its caller.
func TestFakeTimerStopPreventsAFutureFire(t *testing.T) {
	fake := NewFake(epoch)
	timer := fake.NewTimer(5 * time.Millisecond)
	if wasActive := timer.Stop(); !wasActive {
		t.Fatal("Stop() on a never-fired timer returned false, want true")
	}
	fake.Advance(time.Hour)
	select {
	case v := <-timer.C():
		t.Fatalf("stopped timer fired with %v, want no fire ever", v)
	default:
	}
}

// TestFakeTimerResetReschedulesFromNow proves Reset computes the new
// deadline from the clock's CURRENT time, not the timer's original
// registration time.
func TestFakeTimerResetReschedulesFromNow(t *testing.T) {
	fake := NewFake(epoch)
	timer := fake.NewTimer(5 * time.Millisecond)
	fake.Advance(3 * time.Millisecond)
	timer.Reset(5 * time.Millisecond) // now due at epoch+3ms+5ms = epoch+8ms

	fake.Advance(4 * time.Millisecond) // epoch+7ms: not due yet
	select {
	case v := <-timer.C():
		t.Fatalf("reset timer fired early with %v, want still pending at epoch+7ms", v)
	default:
	}

	fake.Advance(1 * time.Millisecond) // epoch+8ms: due now
	select {
	case got := <-timer.C():
		want := epoch.Add(8 * time.Millisecond)
		if !got.Equal(want) {
			t.Fatalf("reset timer fired at %v, want %v", got, want)
		}
	default:
		t.Fatal("reset timer never fired at its rescheduled deadline")
	}
}
