package clock

import (
	"testing"
	"time"
)

// Compile-time assertions that Real and Fake satisfy Clock, and that their
// Timer/Ticker constructors satisfy the Timer/Ticker seams — the whole
// point of the interface is that a caller can hold either without knowing
// which is underneath.
var (
	_ Clock  = Real
	_ Clock  = (*Fake)(nil)
	_ Timer  = (*realTimer)(nil)
	_ Timer  = (*fakeTimer)(nil)
	_ Ticker = (*realTicker)(nil)
	_ Ticker = (*fakeTicker)(nil)
)

// TestNewFakeReadsTheGivenStart pins the one thing clock_test.go itself
// needs to prove for C13 (every source file carries its own test): a fresh
// Fake reads back exactly the start time it was given, not a zero value or
// the real wall clock.
func TestNewFakeReadsTheGivenStart(t *testing.T) {
	start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	fake := NewFake(start)
	if got := fake.Now(); !got.Equal(start) {
		t.Fatalf("Now() = %v, want %v", got, start)
	}
}
