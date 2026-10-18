package ui

import "testing"

// TestCarouselCompactShowsOnlyTheCurrentActionFilled pins the one-action form:
// the current action alone, labelled, in its filled box, its index wrapping in
// both directions so → and ← cycle without leaving the action set.
func TestCarouselCompactShowsOnlyTheCurrentActionFilled(t *testing.T) {
	count := len(carouselActions)
	for _, testCase := range []struct{ index, want int }{
		{0, 0},
		{count - 1, count - 1},
		{count, 0},
		{-1, count - 1},
	} {
		action := carouselActions[testCase.want]
		want := "◖" + action.Glyph + " " + action.Label + "◗"
		if got := carouselCompact(testCase.index); got != want {
			t.Errorf("carouselCompact(%d) = %q, want %q", testCase.index, got, want)
		}
	}
}
