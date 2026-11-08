package herdr

import (
	"context"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

// TestShieldHoldListsOncePerPass walks one shield through freshness, a held
// pass, a nested hold and the prune at a pass's start, counting herdr-shield
// runs after every read.
func TestShieldHoldListsOncePerPass(t *testing.T) {
	runner := shieldRunner(t, shieldListing("running", true, false, "PROTECTED"), 0)
	fake := clock.NewFake(time.Date(2026, 11, 8, 2, 0, 0, 0, time.UTC))
	shield := &Shield{Runner: runner, Clock: fake}
	var release, inner func()
	for _, step := range []struct {
		name     string
		advance  time.Duration
		act      func()
		wantRuns int
	}{
		{name: "first read lists", wantRuns: 1},
		{name: "a read just inside the window reuses it", advance: shieldListFresh - time.Nanosecond, wantRuns: 1},
		{name: "a read at the window's end lists again", advance: time.Nanosecond, wantRuns: 2},
		{
			name: "a held pass keeps the listing past its window", advance: 5 * time.Second,
			act: func() { release = shield.Hold() }, wantRuns: 2,
		},
		{
			name: "a nested hold released twice leaves the outer one holding",
			act:  func() { inner = shield.Hold(); inner(); inner() }, wantRuns: 2,
		},
		{name: "the released pass's stale listing lists again", act: func() { release(); release() }, wantRuns: 3},
		{
			name: "a pass started just inside the window keeps that listing", advance: 10 * time.Second,
			act: func() {
				fake.Advance(shieldListFresh - time.Nanosecond)
				release = shield.Hold()
			},
			wantRuns: 3,
		},
		{name: "a read after the pass lists again", act: func() { release() }, wantRuns: 4},
		{
			name: "a pass started at the window's end drops that listing",
			act: func() {
				fake.Advance(shieldListFresh)
				release = shield.Hold()
			},
			wantRuns: 5,
		},
	} {
		if step.act != nil {
			step.act()
		}
		fake.Advance(step.advance)
		holders, err := shield.Running(context.Background(), shieldSocket)
		if err != nil || len(holders) != 1 || holders[0].ID != "h1" {
			t.Fatalf("%s: Running(%s) = %+v, %v; want holder h1", step.name, shieldSocket, holders, err)
		}
		if runs := len(runner.Calls()); runs != step.wantRuns {
			t.Fatalf("%s: herdr-shield ran %d times in all, want %d", step.name, runs, step.wantRuns)
		}
	}
	release()

	// A failed listing holds only while fresh: a held pass retries it once stale.
	failing := shieldRunner(t, "herdr-shield: server busy", 1)
	broken := &Shield{Runner: failing, Clock: fake}
	release = broken.Hold()
	for _, step := range []struct {
		name     string
		advance  time.Duration
		wantRuns int
	}{
		{name: "a failed listing", wantRuns: 1},
		{name: "a held read inside the window reuses the failure", advance: shieldListFresh - time.Nanosecond, wantRuns: 1},
		{name: "a held read at the window's end retries", advance: time.Nanosecond, wantRuns: 2},
	} {
		fake.Advance(step.advance)
		const want = "herdr-shield list --json against " + shieldSocket + ": exit 1: herdr-shield: server busy"
		if _, err := broken.Running(context.Background(), shieldSocket); err == nil || err.Error() != want {
			t.Fatalf("%s: Running(%s) = %v; want %s", step.name, shieldSocket, err, want)
		}
		if runs := len(failing.Calls()); runs != step.wantRuns {
			t.Fatalf("%s: herdr-shield ran %d times in all, want %d", step.name, runs, step.wantRuns)
		}
	}
	release()

	var unwired *Shield
	unwired.Hold()()
	if _, err := unwired.Running(context.Background(), shieldSocket); err == nil ||
		err.Error() != "no herdr-shield reader is wired here" {
		t.Fatalf("Running on an unwired shield under a hold = %v; want no herdr-shield reader is wired here", err)
	}
}
