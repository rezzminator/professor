package herdr

import (
	"context"
	"errors"
	"slices"
	"testing"
)

// TestTabObservedComparesEveryPaintedField pins which observations a read that
// is not fresh leaves unreconciled: only one the last read held field for field,
// whatever Validate closure it carries.
func TestTabObservedComparesEveryPaintedField(t *testing.T) {
	previous := []TabDecoration{tabObservation("idle")}
	revalidated := tabObservation("idle")
	revalidated.Validate = func(context.Context) (bool, error) { return false, nil }
	renamed := tabObservation("idle")
	renamed.Name = "Renamed"
	elsewhere := tabObservation("idle")
	elsewhere.Tab = "w1:t2"
	reglyphed := tabObservation("idle")
	reglyphed.Glyphs.Idle = TabFrames{"x"}
	for _, tc := range []struct {
		name        string
		previous    []TabDecoration
		observation TabDecoration
		want        bool
	}{
		{"the same observation with a fresh Validate", previous, revalidated, true},
		{"renamed", previous, renamed, false},
		{"another tab", previous, elsewhere, false},
		{"other glyphs", previous, reglyphed, false},
		{"another state", previous, tabObservation("working"), false},
		{"no read before", nil, revalidated, false},
	} {
		if got := tabObserved(tc.previous, tc.observation); got != tc.want {
			t.Errorf("%s: tabObserved = %t, want %t", tc.name, got, tc.want)
		}
	}
}

// TestAStaticTabWhoseReconcileFailedIsRetriedUntilItSettles pins that a read
// that is not fresh skips an unchanged static tab only once its last
// reconcile succeeded: a failed rename is retried at the next tick, not left
// until the next fresh read.
func TestAStaticTabWhoseReconcileFailedIsRetriedUntilItSettles(t *testing.T) {
	f := newTabStatusFixture()
	painter, _ := tabPainterFixture(t, f)
	validates := 0
	observation := tabObservation("idle")
	observation.Validate = func(context.Context) (bool, error) {
		validates++
		if validates == 1 {
			return false, errors.New("owner read refused")
		}
		return true, nil
	}
	ctx := context.Background()
	for tick := range 3 { // the fresh paint the owner read fails, then two that are not fresh
		if _, err := painter.paintTabs(ctx, []TabDecoration{observation}, int64(tick), tick == 0); err != nil {
			t.Fatalf("tick %d: %v; want an unproved owner left untouched, named, never an error", tick, err)
		}
	}
	if validates != 2 || !slices.Equal(f.renames, []string{"⚪ Chat"}) {
		t.Fatalf("validates %d, renames %q; want 2 and [\"⚪ Chat\"]: the failed tab retried once, then settled",
			validates, f.renames)
	}
}
