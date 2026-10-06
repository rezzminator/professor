package ui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestActivityClockNilReadsAsUnstamped(t *testing.T) {
	var clock *ActivityClock
	clock.Stamp(time.Now())
	if stamp := clock.StampNS(); stamp != 0 {
		t.Fatalf("nil clock StampNS = %d, want 0", stamp)
	}
}

func TestActivityClockOpensStampedWithoutWake(t *testing.T) {
	clock := NewActivityClock(time.Unix(1_800_000_000, 0))
	if got := clock.StampNS(); got != 1800000000000000000 {
		t.Fatalf("opening stamp = %d, want 1800000000000000000", got)
	}
	if got := len(clock.Wake()); got != 0 {
		t.Fatalf("opening wake tokens = %d, want 0 before a keypress", got)
	}
	clock.Stamp(time.Unix(1_800_000_001, 0))
	if got := len(clock.Wake()); got != 1 {
		t.Fatalf("keypress wake tokens = %d, want 1", got)
	}
}

func TestActivityClockStampChangesOnInteraction(t *testing.T) {
	start := time.Now()
	clock := NewActivityClock(start)
	first := clock.StampNS()
	if first != start.UnixNano() {
		t.Fatalf("StampNS = %d, want %d", first, start.UnixNano())
	}
	clock.Stamp(start.Add(time.Second))
	if second := clock.StampNS(); second == first {
		t.Fatalf("StampNS unchanged (%d) after a second interaction", second)
	}
}

// TestModelKeypressStampsActivity closes the loop the backoff depends on. The
// refresh stream slows down on an idle clock, so if the model never stamped,
// a picker would go sluggish in the hands of somebody actively driving it —
// a worse fault than the CPU burn the backoff exists to fix.
func TestModelKeypressStampsActivity(t *testing.T) {
	stale := time.Now().Add(-30 * time.Minute)
	clock := NewActivityClock(stale)
	model := NewModel(Snapshot{Activity: clock})
	before := clock.StampNS()

	model.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})

	if after := clock.StampNS(); after == before {
		t.Fatal(
			"activity stamp unchanged after a keypress: the model is not " +
				"stamping, so the refresh stream will keep backing off under " +
				"an active user",
		)
	}
}
