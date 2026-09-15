package ui

import (
	"testing"
	"time"
)

// TestSkyTickCadenceGrowsWhileUntouched pins the arithmetic tickCadence
// drives the ambient sky/cosmos header tick with — the same shape as
// cmd/pfm's TestRefreshCadenceGrowsWhileUntouched for the background fleet
// scan, at the sky tick's own (steeper) base and growth.
func TestSkyTickCadenceGrowsWhileUntouched(t *testing.T) {
	clock := NewActivityClock(time.Now())
	cadence := newTickCadence(clock, skyTickBaseInterval, skyTickGrowth, skyTickParkThreshold)

	if cadence.interval != skyTickBaseInterval {
		t.Fatalf("opening interval = %s, want %s", cadence.interval, skyTickBaseInterval)
	}

	want := []time.Duration{
		168750000 * time.Nanosecond,
		227812500 * time.Nanosecond,
		307546875 * time.Nanosecond,
	}
	for step, expected := range want {
		got := cadence.next()
		if got != expected {
			t.Fatalf("interval after %d untouched ticks = %s, want %s", step+1, got, expected)
		}
	}
}

// TestSkyTickCadenceResetsOnInteraction proves a keystroke snaps the sky
// tick back to full cadence rather than leaving an actively-watched picker
// stuck at whatever interval an earlier idle stretch grew to.
func TestSkyTickCadenceResetsOnInteraction(t *testing.T) {
	clock := NewActivityClock(time.Now())
	cadence := newTickCadence(clock, skyTickBaseInterval, skyTickGrowth, skyTickParkThreshold)
	for range 5 {
		cadence.next()
	}
	if cadence.interval <= skyTickBaseInterval {
		t.Fatalf("interval after 5 untouched ticks = %s, want > %s", cadence.interval, skyTickBaseInterval)
	}

	clock.Stamp(time.Now().Add(time.Second))
	if got := cadence.next(); got != skyTickBaseInterval {
		t.Fatalf(
			"interval after a keystroke = %s, want %s: a picker under an "+
				"active user must snap back to full cadence",
			got, skyTickBaseInterval,
		)
	}
}

// TestSkyTickCadenceParkThresholdAndNilClockNeverBacksOff proves the sky
// tick's cadence never exceeds skyTickParkThreshold — the model layer treats
// reaching it as "stop scheduling entirely" (TestSkyTickMsgParksThenWakes) —
// and that a nil activity clock, every non-interactive Model and every
// existing render/golden test in this package, reads as permanently active
// rather than silently freezing the animation.
func TestSkyTickCadenceParkThresholdAndNilClockNeverBacksOff(t *testing.T) {
	cadence := newTickCadence(NewActivityClock(time.Now()), skyTickBaseInterval, skyTickGrowth, skyTickParkThreshold)
	for range 50 {
		if got := cadence.next(); got > skyTickParkThreshold {
			t.Fatalf("interval %s exceeded the park threshold %s", got, skyTickParkThreshold)
		}
	}
	if cadence.interval != skyTickParkThreshold {
		t.Fatalf("interval after 50 ticks = %s, want the threshold %s", cadence.interval, skyTickParkThreshold)
	}

	nilCadence := newTickCadence(nil, skyTickBaseInterval, skyTickGrowth, skyTickParkThreshold)
	for range 50 {
		if got := nilCadence.next(); got != skyTickBaseInterval {
			t.Fatalf("nil-clock interval = %s, want a steady %s", got, skyTickBaseInterval)
		}
	}
}

// TestSkyTickMsgStretchesLiveCadence pins the fix in the LIVE Update loop,
// not just in the cadence arithmetic — the sky tick used to reschedule
// itself on a flat 125ms literal forever, which would pass every test above
// while still burning a picker abandoned in a VS Code tab (2026-09-03: four
// idle pickers measured at 36-100% CPU each, 15 minutes untouched). Only
// this test proves the running model actually ASKS skyCadence for the next
// interval on every tick (while still short of the park threshold), and
// that a keystroke resets it back to full cadence for whichever tick comes
// next.
func TestSkyTickMsgStretchesLiveCadence(t *testing.T) {
	clock := NewActivityClock(time.Now())
	model := NewModel(Snapshot{Activity: clock, NowNS: 1})
	if !model.skyEnabled {
		t.Fatal("sky disabled by default — fixture assumption broken")
	}

	var previous time.Duration
	for step := 0; step < 4; step++ {
		updated, cmd := model.Update(skyTickMsg{nowNS: model.nowNS + 1})
		var ok bool
		model, ok = updated.(Model)
		if !ok {
			t.Fatalf("Update returned %T", updated)
		}
		if cmd == nil {
			t.Fatalf("pass %d: sky tick did not reschedule (still under the park threshold)", step)
		}
		if model.skyParked {
			t.Fatalf("pass %d: parked before reaching skyTickParkThreshold", step)
		}
		if step > 0 && model.skyCadence.interval <= previous {
			t.Fatalf(
				"pass %d: cadence interval = %s, want > previous pass's %s — "+
					"an untouched sky tick must stretch",
				step, model.skyCadence.interval, previous,
			)
		}
		previous = model.skyCadence.interval
	}

	// A keystroke mid-stream must reset the NEXT tick to full cadence, the
	// same way an active picker keeps the fleet refresh prompt.
	model, _ = applyKey(t, model, printableKey('j'))
	updated, cmd := model.Update(skyTickMsg{nowNS: model.nowNS + 1})
	model, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update returned %T", updated)
	}
	if cmd == nil {
		t.Fatal("sky tick after keypress did not reschedule")
	}
	if model.skyCadence.interval != skyTickBaseInterval {
		t.Fatalf(
			"cadence interval after a keypress = %s, want %s: an active "+
				"picker must not stay backed off",
			model.skyCadence.interval, skyTickBaseInterval,
		)
	}
}

// TestSkyTickMsgParksThenWakes proves the loop actually STOPS — the literal
// ask in the brief ("stop the cosmos/sky widget animation when idle") —
// rather than merely ticking slower forever, and that a keystroke restarts
// it immediately rather than waiting for an already-scheduled, far-future
// tick to notice.
func TestSkyTickMsgParksThenWakes(t *testing.T) {
	clock := NewActivityClock(time.Now())
	model := NewModel(Snapshot{Activity: clock, NowNS: 1})

	parkedAt := -1
	for step := 0; step < 20; step++ {
		updated, cmd := model.Update(skyTickMsg{nowNS: model.nowNS + 1})
		var ok bool
		model, ok = updated.(Model)
		if !ok {
			t.Fatalf("Update returned %T", updated)
		}
		if cmd == nil {
			if !model.skyParked {
				t.Fatalf("pass %d: no reschedule but skyParked is false", step)
			}
			parkedAt = step
			break
		}
		if model.skyParked {
			t.Fatalf("pass %d: skyParked true despite a live reschedule", step)
		}
	}
	if parkedAt < 0 {
		t.Fatal("sky tick never parked within 20 untouched passes")
	}

	// Parked: a further sky tick must never arrive (there is nothing
	// scheduled to produce one), and the model must stay parked.
	updated, cmd := model.Update(skyTickMsg{nowNS: model.nowNS + 1})
	model, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update returned %T", updated)
	}
	if cmd != nil {
		t.Fatal("a stray sky tick rescheduled itself while parked")
	}
	if !model.skyParked {
		t.Fatal("a stray sky tick cleared skyParked")
	}

	// A keystroke wakes it — instantly, not by waiting on a stale timer that
	// was never scheduled in the first place.
	model, wake := applyKey(t, model, printableKey('j'))
	if wake == nil {
		t.Fatal("keypress produced no command to un-park the sky tick")
	}
	if model.skyParked {
		t.Fatal("skyParked still true immediately after the waking keypress")
	}
	if msg := wake(); msg == nil {
		t.Fatal("wake command produced no message")
	} else if _, ok := msg.(skyTickMsg); !ok {
		t.Fatalf("wake command produced %T, want skyTickMsg", msg)
	}
	if model.skyCadence.interval != skyTickBaseInterval {
		t.Fatalf(
			"cadence interval after waking = %s, want %s",
			model.skyCadence.interval, skyTickBaseInterval,
		)
	}
}

// TestNoSkyModelNeverSchedulesSkyTick proves --no-sky (skyEnabled false)
// stops the animation loop outright rather than merely slowing it — Init
// keeps only the independent wall clock, and a stray skyTickMsg (there should
// never be one, but the case exists) is a no-op rather than a reschedule.
func TestNoSkyModelNeverSchedulesSkyTick(t *testing.T) {
	snapshot := fixtureSnapshot(120)
	snapshot.NoSky = true
	model := NewModel(snapshot)

	if cmd := model.Init(); cmd == nil {
		t.Fatal("Init did not schedule the independent wall clock under --no-sky")
	}

	future := model.nowNS + int64(2*time.Hour)
	updated, cmd := model.Update(clockTickMsg{nowNS: future})
	model = updated.(Model)
	if cmd == nil || model.nowNS != future || model.cosmosNowNS != future {
		t.Fatalf(
			"wall clock under --no-sky: command=%v now=%d cosmosNow=%d want=%d",
			cmd,
			model.nowNS,
			model.cosmosNowNS,
			future,
		)
	}

	updated, cmd = model.Update(skyTickMsg{nowNS: future + 1})
	model = updated.(Model)
	if cmd != nil {
		t.Fatal("a stray sky tick rescheduled itself under --no-sky")
	}
	if model.nowNS != future || model.cosmosNowNS != future {
		t.Fatalf("a stray sky tick changed --no-sky clocks: now=%d cosmosNow=%d", model.nowNS, model.cosmosNowNS)
	}
}
