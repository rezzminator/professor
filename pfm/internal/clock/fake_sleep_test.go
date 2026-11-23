package clock_test

// fake_sleep_test.go: the fake's Sleep proven through the wait door, which the internal test package
// cannot import (testwait imports clock).

import (
	"context"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

// TestFakeSleepBlocksUntilAdvanceReleasesIt proves the promised contract: a
// goroutine calling Sleep on a Fake blocks until another goroutine calls
// Advance past its duration.
func TestFakeSleepBlocksUntilAdvanceReleasesIt(t *testing.T) {
	fake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	done := make(chan error, 1)
	go func() {
		done <- fake.Sleep(context.Background(), 20*time.Millisecond)
	}()

	testwait.Until(t, "Sleep's waiter registered on the fake", func() (bool, string) {
		return fake.Pending() == 1, fake.String()
	})
	fake.Advance(19 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("Sleep returned (%v) before an Advance reached its duration", err)
	default:
	}

	fake.Advance(time.Millisecond)
	if err := <-done; err != nil {
		t.Fatalf("Sleep() error = %v, want nil", err)
	}
}
