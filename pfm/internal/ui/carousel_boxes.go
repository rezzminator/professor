package ui

import (
	"strings"
)

// carouselActions is the action set a selected chat row offers, in cycle order.
// Each carries its label: an emoji alone leaves the row guessing what ⚡ does.
var carouselActions = []struct{ Glyph, Label string }{
	{"▶", "open"},
	{"⚡", "reboot"},
	{"🕐", "1h"},
	{"✖", "kill"},
	{"⏸", "deactive"},
}

// carouselBoxes draws every action as its own labelled box at the row's right
// edge, the current one filled with block edges instead of light brackets. The
// whole set is visible the moment a row is highlighted — a single box whose
// contents change conceals three of the four actions behind blind → presses, and
// the fill marks the selection without colour, which the row-wide selected
// style would otherwise swallow.
func carouselBoxes(index int) string {
	boxes := make([]string, 0, len(carouselActions))
	for position, action := range carouselActions {
		body := action.Glyph + " " + action.Label
		if position == index {
			boxes = append(boxes, "◖"+body+"◗")
			continue
		}
		boxes = append(boxes, "["+body+"]")
	}
	return strings.Join(boxes, " ")
}

// carouselCompact is the one-action form for a row with no dossier beside it:
// only the current action, in its box, so the row keeps its name.
func carouselCompact(index int) string {
	count := len(carouselActions)
	action := carouselActions[((index%count)+count)%count]
	return "◖" + action.Glyph + " " + action.Label + "◗"
}
