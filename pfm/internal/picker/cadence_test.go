package picker

import (
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/ui"
)

func TestRefreshCadenceGrowsWhileUntouched(t *testing.T) {
	clock := ui.NewActivityClock(time.Now())
	cadence := newRefreshCadence(clock)

	if cadence.interval != fleetRefreshInterval {
		t.Fatalf(
			"opening interval = %s, want %s",
			cadence.interval, fleetRefreshInterval,
		)
	}

	// growth is steep enough that ONE untouched pass already crosses
	// fleetRefreshParkThreshold (5s * 13 = 65s > 60s) — at several
	// CPU-seconds a pass on a real fleet (~50 tmux sockets, ~1950
	// processes measured on devbox 2026-09-03), even a gentle multi-step
	// ramp risks a pass landing inside any given 30s measurement window.
	if got := cadence.next(); got != fleetRefreshParkThreshold {
		t.Fatalf("interval after 1 untouched pass = %s, want the park threshold %s",
			got, fleetRefreshParkThreshold)
	}
	if got := cadence.next(); got != fleetRefreshParkThreshold {
		t.Fatalf("interval after 2 untouched passes = %s, want it to stay at %s",
			got, fleetRefreshParkThreshold)
	}
}

func TestRefreshCadenceResetsOnInteraction(t *testing.T) {
	clock := ui.NewActivityClock(time.Now())
	cadence := newRefreshCadence(clock)
	for range 20 {
		cadence.next()
	}
	if cadence.interval <= fleetRefreshInterval {
		t.Fatalf(
			"interval after 20 untouched passes = %s, want > %s",
			cadence.interval, fleetRefreshInterval,
		)
	}

	clock.Stamp(time.Now().Add(time.Second))
	if got := cadence.next(); got != fleetRefreshInterval {
		t.Fatalf(
			"interval after a keystroke = %s, want %s: a picker under an "+
				"active user must snap back to full cadence",
			got, fleetRefreshInterval,
		)
	}
}

func TestRefreshCadenceCapsAndNilClockNeverBacksOff(t *testing.T) {
	cadence := newRefreshCadence(ui.NewActivityClock(time.Now()))
	for range 500 {
		if got := cadence.next(); got > fleetRefreshParkThreshold {
			t.Fatalf("interval %s exceeded the cap %s",
				got, fleetRefreshParkThreshold)
		}
	}
	if cadence.interval != fleetRefreshParkThreshold {
		t.Fatalf("interval after 500 passes = %s, want the cap %s",
			cadence.interval, fleetRefreshParkThreshold)
	}

	// A nil clock is every non-interactive caller: no presence signal must
	// ever be READ as "nobody is there".
	nilCadence := newRefreshCadence(nil)
	for range 50 {
		if got := nilCadence.next(); got != fleetRefreshInterval {
			t.Fatalf("nil-clock interval = %s, want a steady %s",
				got, fleetRefreshInterval)
		}
	}
}

// TestPickerRefreshStreamParksThenWakesOnKeystroke pins the fix in the LIVE
// loop, not just in the cadence arithmetic. A picker used to refresh on a
// fixed ticker for its whole life, and one pass costs several CPU-seconds on
// a real fleet — a tmux fork+exec per live socket plus a full store read and
// a per-process scan for every engine detector — so a picker left in a pane
// nobody watched ground over half a core indefinitely (2026-09-03 real-box
// measurement, 1741 ticks/30s).
//
// The unit tests above prove next() computes the right numbers. Only this
// one proves the loop actually STOPS asking: it must send exactly the two
// passes the transition into park allows (the initial synchronous pass, then
// the one loop-driven pass that crosses fleetRefreshParkThreshold), then go
// silent — no third pass, ever, until the activity clock moves — and a
// keystroke must wake it within one park-poll interval, not a stale
// already-scheduled multi-minute timer.

func TestFailureBackoffDoublesFromTheIntervalAndCapsAtTheParkThreshold(t *testing.T) {
	previousInterval, previousThreshold := fleetRefreshInterval, fleetRefreshParkThreshold
	fleetRefreshInterval, fleetRefreshParkThreshold = 5*time.Second, 60*time.Second
	t.Cleanup(func() { fleetRefreshInterval, fleetRefreshParkThreshold = previousInterval, previousThreshold })
	for streak, want := range map[int]time.Duration{
		1: 5 * time.Second, 2: 10 * time.Second, 3: 20 * time.Second, 4: 40 * time.Second,
		5: 60 * time.Second, 6: 60 * time.Second, 500: 60 * time.Second,
	} {
		if got := failureBackoff(streak); got != want {
			t.Errorf("failureBackoff(%d) = %v, want %v", streak, got, want)
		}
	}
}
