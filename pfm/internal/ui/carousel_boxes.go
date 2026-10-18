package ui

// carouselActions is the action set a selected chat row offers, in cycle order.
// Each carries its label: an emoji alone leaves the row guessing what ⚡ does.
var carouselActions = []struct{ Glyph, Label string }{
	{"▶", "open"},
	{"⚡", "reboot"},
	{"✖", "kill"},
	{"⏸", "deactive"},
}

// carouselCompact is the one-action form for a row with no dossier beside it:
// only the current action, in its box, so the row keeps its name.
func carouselCompact(index int) string {
	count := len(carouselActions)
	action := carouselActions[((index%count)+count)%count]
	return "◖" + action.Glyph + " " + action.Label + "◗"
}
