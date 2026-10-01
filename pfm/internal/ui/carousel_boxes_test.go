package ui

import (
	"strings"
	"testing"
)

// TestCarouselBoxesShowsEveryActionAtOnce pins the contract that highlighting a
// chat reveals the whole action set, labelled — not one box whose glyph changes
// as → is pressed.
func TestCarouselBoxesShowsEveryActionAtOnce(t *testing.T) {
	boxes := carouselBoxes(0)
	for _, label := range []string{"open", "reboot", "1h", "kill", "deactive"} {
		if !strings.Contains(boxes, label) {
			t.Fatalf("carouselBoxes(0) = %q, missing the %q action", boxes, label)
		}
	}
	if strings.Count(boxes, "[")+strings.Count(boxes, "◖") != len(carouselActions) {
		t.Fatalf("carouselBoxes(0) = %q, want one box per action", boxes)
	}
}

// TestCarouselBoxesFillsOnlyTheCurrentAction proves the selection is marked by
// the filled box moving, while every action keeps its own box and label.
func TestCarouselBoxesFillsOnlyTheCurrentAction(t *testing.T) {
	for index, want := range map[int]string{
		0: "◖▶ open◗",
		1: "◖⚡ reboot◗",
		3: "◖✖ kill◗",
		4: "◖⏸ deactive◗",
	} {
		boxes := carouselBoxes(index)
		if !strings.Contains(boxes, want) {
			t.Fatalf("carouselBoxes(%d) = %q, want it to fill %q", index, boxes, want)
		}
		if filled := strings.Count(boxes, "◖"); filled != 1 {
			t.Fatalf("carouselBoxes(%d) = %q, filled %d boxes, want 1", index, boxes, filled)
		}
		if light := strings.Count(boxes, "["); light != len(carouselActions)-1 {
			t.Fatalf("carouselBoxes(%d) = %q, %d light boxes, want %d",
				index, boxes, light, len(carouselActions)-1)
		}
	}
}
