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
// reconcile settled it: a failed owner read, an owner not proved for a tick
// and a pane read past the observation are each retried at the next tick, not
// left until the next fresh read.
func TestAStaticTabWhoseReconcileFailedIsRetriedUntilItSettles(t *testing.T) {
	for _, tc := range []struct {
		name  string
		first func(*tabStatusFixture) (bool, error) // the first owner read; later ones prove it
	}{
		{"the owner read refused", func(*tabStatusFixture) (bool, error) {
			return false, errors.New("owner read refused")
		}},
		{"the owner not proved", func(*tabStatusFixture) (bool, error) { return false, nil }},
		{"the pane read past the observation", func(f *tabStatusFixture) (bool, error) {
			f.state = "working" // the pane read right after differs from the observed blocked
			return true, nil
		}},
	} {
		f := newTabStatusFixture()
		painter, _ := tabPainterFixture(t, f)
		validates := 0
		observation := tabObservation("idle")
		observation.Validate = func(context.Context) (bool, error) {
			validates++
			if validates == 1 {
				return tc.first(f)
			}
			f.state = "blocked"
			return true, nil
		}
		ctx := context.Background()
		for tick := range 3 { // the fresh paint the first owner read spoils, then two that are not fresh
			if _, err := painter.paintTabs(ctx, []TabDecoration{observation}, int64(tick), tick == 0); err != nil {
				t.Fatalf("%s: tick %d: %v; want an unproved owner left untouched, named, never an error",
					tc.name, tick, err)
			}
		}
		if validates != 2 || !slices.Equal(f.renames, []string{"⚪ Chat"}) {
			t.Errorf("%s: validates %d, renames %q; want 2 and [\"⚪ Chat\"]: the tab retried once, then settled",
				tc.name, validates, f.renames)
		}
	}
}
