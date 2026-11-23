package themesync

import (
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

// TestClockOrReadsTheSeamElseTheRealClockAtTheCall: a seam a test set is the clock read, so its time is the
// one a stamp or a latency takes, moved by the test alone; an unset seam is clock.Real, read at the call.
func TestClockOrReadsTheSeamElseTheRealClockAtTheCall(t *testing.T) {
	start := time.Date(2026, 11, 23, 2, 0, 0, 0, time.UTC)
	fake := clock.NewFake(start)
	for _, row := range []struct {
		name    string
		advance time.Duration
		want    time.Time
	}{
		{"as set", 0, start},
		{"moved by the test", 1500 * time.Millisecond, start.Add(1500 * time.Millisecond)},
	} {
		fake.Advance(row.advance)
		if got := clockOr(fake).Now(); !got.Equal(row.want) {
			t.Errorf("%s: clockOr(fake).Now() = %s, want %s", row.name, got, row.want)
		}
	}
	if got := clockOr(nil); got != clock.Real {
		t.Errorf("clockOr(nil) = %#v, want clock.Real", got)
	}
}
