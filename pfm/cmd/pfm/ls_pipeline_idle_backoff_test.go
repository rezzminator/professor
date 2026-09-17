package main

import (
	"bytes"
	"context"
	"testing"
	"time"

	"hostops/pfm/internal/fleet"
	"hostops/pfm/internal/store"
	"hostops/pfm/internal/ui"
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
func TestPickerRefreshStreamParksThenWakesOnKeystroke(t *testing.T) {
	shortenRefreshIntervals(t)
	jailTest(t)

	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates := make(chan ui.Snapshot, 1)
	var stderr bytes.Buffer
	runner := &immediateIndexRunner{}
	clock := ui.NewActivityClock(time.Now())
	go streamFleetRefreshesWith(
		ctx,
		database,
		scanRequest{},
		fleet.PrintWarn(&stderr),
		&stderr,
		updates,
		refreshDependencies{
			newIndexer: func(*store.Store) (indexRunner, error) {
				return runner, nil
			},
			activity: clock,
		},
	)

	completed := 0
	starve := time.After(20 * time.Second)
	for completed < 2 {
		select {
		case snapshot, ok := <-updates:
			if !ok {
				t.Fatalf("refresh stream closed after %d of 2 passes: %s", completed, stderr.String())
			}
			if !snapshot.Refreshing {
				completed++
			}
		case <-starve:
			t.Fatalf("refresh stream produced %d of 2 passes in 20s: %s", completed, stderr.String())
		}
	}

	// Parked: no further pass for well over several fleetRefreshParkPollInterval
	// ticks proves the loop actually stopped, not merely slowed to something
	// this test's patience could still outlast.
	select {
	case snapshot, ok := <-updates:
		if ok {
			t.Fatalf("a third pass arrived while parked and untouched: %#v", snapshot)
		}
	case <-time.After(50 * time.Millisecond):
	}

	// A keystroke must wake it inside a poll or two — not wait on an
	// already-scheduled timer that was never armed for anything this soon.
	clock.Stamp(time.Now())
	select {
	case _, ok := <-updates:
		if !ok {
			t.Fatal("refresh stream closed instead of waking on a keystroke")
		}
	case <-time.After(time.Second):
		t.Fatal("no pass arrived within 1s of a keystroke while parked")
	}
	cancel()
	for range updates {
	}
}
