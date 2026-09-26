package ui

import (
	"testing"
	"time"
)

// TestStatsCadenceGrowsAndCapsWhileUntouched pins the Limits tab's own idle
// backoff arithmetic (statsRefreshGrowth/statsRefreshMaxInterval) — the
// last periodic loop in the picker that ticked on a flat 2s literal forever
// regardless of activity (2026-09-08 measurement, devbox: an idle Limits tab
// re-armed a Codex provider sampler that execs `codex app-server` every
// tick). The shape mirrors cmd/pfm's TestRefreshCadenceGrowsWhileUntouched
// and this package's own TestSkyTickCadenceGrowsWhileUntouched.
func TestStatsCadenceGrowsAndCapsWhileUntouched(t *testing.T) {
	clock := NewActivityClock(time.Now())
	cadence := newTickCadence(clock, statsRefreshInterval, statsRefreshMaxInterval)

	if cadence.interval != statsRefreshInterval {
		t.Fatalf("opening interval = %s, want %s", cadence.interval, statsRefreshInterval)
	}
	var previous time.Duration
	for step := 0; step < 40; step++ {
		got := cadence.next()
		if got > statsRefreshMaxInterval {
			t.Fatalf("step %d: interval %s exceeded the cap %s", step, got, statsRefreshMaxInterval)
		}
		if step > 0 && got < previous {
			t.Fatalf("step %d: interval %s shrank from %s without an interaction", step, got, previous)
		}
		previous = got
	}
	if cadence.interval != statsRefreshMaxInterval {
		t.Fatalf("interval after 40 untouched steps = %s, want the cap %s", cadence.interval, statsRefreshMaxInterval)
	}
}

// TestStatsCadenceResetsOnInteraction proves a keystroke snaps the Limits
// tab's sample cadence back to full speed rather than leaving an actively
// watched picker stuck at whatever an earlier idle stretch grew it to.
func TestStatsCadenceResetsOnInteraction(t *testing.T) {
	clock := NewActivityClock(time.Now())
	cadence := newTickCadence(clock, statsRefreshInterval, statsRefreshMaxInterval)
	for range 10 {
		cadence.next()
	}
	if cadence.interval <= statsRefreshInterval {
		t.Fatalf("interval after 10 untouched passes = %s, want > %s", cadence.interval, statsRefreshInterval)
	}

	clock.Stamp(time.Now().Add(time.Second))
	if got := cadence.next(); got != statsRefreshInterval {
		t.Fatalf(
			"interval after a keystroke = %s, want %s: an active picker must snap back to full cadence",
			got, statsRefreshInterval,
		)
	}
}

// TestStatsSampleMsgStretchesOnlyTheLimitsTab is the live-Update-loop proof,
// not just the cadence arithmetic: only the Limits tab's own statsSampleMsg
// handling asks statsCadence for the next interval and lets it grow: the
// Stats tab's live CPU/memory bars stay pinned to the flat
// statsRefreshInterval by design (resourcesOnly — see model.go), and the
// live cadence must snap back to base on a keypress.
func TestStatsSampleMsgStretchesOnlyTheLimitsTab(t *testing.T) {
	snapshot := fixtureSnapshot(120)
	snapshot.NoSky = true
	snapshot.Activity = NewActivityClock(time.Now())
	model := NewModel(snapshot)
	model.tab = TabLimits
	model.statsGeneration = 7

	var previous time.Duration
	for step := 0; step < 4; step++ {
		updated, cmd := model.Update(statsSampleMsg{generation: 7})
		var ok bool
		model, ok = updated.(Model)
		if !ok {
			t.Fatalf("Update returned %T", updated)
		}
		if cmd == nil {
			t.Fatalf("pass %d: statsSampleMsg on TabLimits produced no reschedule", step)
		}
		if step > 0 && model.statsCadence.interval <= previous {
			t.Fatalf(
				"pass %d: statsCadence interval = %s, want > previous pass's %s — an untouched Limits tab must stretch",
				step, model.statsCadence.interval, previous,
			)
		}
		previous = model.statsCadence.interval
	}
	if previous <= statsRefreshInterval {
		t.Fatalf(
			"statsCadence interval after 4 untouched Limits passes = %s, want > %s",
			previous,
			statsRefreshInterval,
		)
	}

	// A keystroke resets the cadence for the NEXT sample.
	model, _ = applyKey(t, model, printableKey('j'))
	updated, cmd := model.Update(statsSampleMsg{generation: model.statsGeneration})
	model, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update returned %T", updated)
	}
	if cmd == nil {
		t.Fatal("statsSampleMsg after a keypress produced no reschedule")
	}
	if model.statsCadence.interval != statsRefreshInterval {
		t.Fatalf(
			"statsCadence interval after a keypress = %s, want %s: an active picker must not stay backed off",
			model.statsCadence.interval, statsRefreshInterval,
		)
	}

	// The Stats tab (resourcesOnly — live CPU/memory bars) must stay flat:
	// statsCadence is never consulted for it, so it must not advance even
	// after many untouched passes.
	model.tab = TabStats
	model.statsGeneration = 9
	frozen := model.statsCadence.interval
	for step := 0; step < 6; step++ {
		updated, cmd := model.Update(statsSampleMsg{generation: 9})
		var ok bool
		model, ok = updated.(Model)
		if !ok {
			t.Fatalf("Update returned %T", updated)
		}
		if cmd == nil {
			t.Fatalf("pass %d: statsSampleMsg on TabStats produced no reschedule", step)
		}
		if model.statsCadence.interval != frozen {
			t.Fatalf(
				"pass %d: statsCadence interval moved to %s on TabStats — the Stats tab must not decay",
				step, model.statsCadence.interval,
			)
		}
	}
}
