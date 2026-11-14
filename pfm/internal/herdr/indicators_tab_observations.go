package herdr

import "reflect"

// The blinker's tab observations: the latest tab read, which tabs animate,
// and which observations a read that is not fresh changed or last failed to
// reconcile, the only static tabs it reconciles.

// tabsAnimate says whether an observed tab plays more than one frame.
func (painter *indicatorPainter) tabsAnimate() bool {
	for i := range painter.tabObservations {
		if painter.tabObservations[i].animates() {
			return true
		}
	}
	return false
}

// swapTabObservations keeps observations as the latest tab read and returns the one before it.
func (painter *indicatorPainter) swapTabObservations(observations []TabDecoration) []TabDecoration {
	previous := painter.tabObservations
	painter.tabObservations = observations
	return previous
}

// tabSettled says whether a read that is not fresh may leave the tab keyed
// key alone: it does not animate, the last read held it unchanged, and its
// last reconcile did not fail, so a refused or failed rename is retried.
func (painter *indicatorPainter) tabSettled(previous []TabDecoration, observation TabDecoration, key string) bool {
	return !observation.animates() && tabObserved(previous, observation) && !painter.tabFailed[key]
}

// settleTab records whether the tab keyed key failed its reconcile and
// returns accepted.
func (painter *indicatorPainter) settleTab(key string, accepted bool, err error) bool {
	if err == nil {
		delete(painter.tabFailed, key)
		return accepted
	}
	if painter.tabFailed == nil {
		painter.tabFailed = map[string]bool{}
	}
	painter.tabFailed[key] = true
	return accepted
}

// tabObserved says whether previous holds observation unchanged: the same tab
// and identity with every painted field equal (Validate is a fresh closure on
// every read, so it never counts).
func tabObserved(previous []TabDecoration, observation TabDecoration) bool {
	observation.Validate = nil
	for i := range previous {
		prior := previous[i]
		prior.Validate = nil
		if reflect.DeepEqual(prior, observation) {
			return true
		}
	}
	return false
}

// animates says whether the tab plays more than one frame.
func (d *TabDecoration) animates() bool {
	return d.Mode != TabStatusOff && d.State != "" && len(tabMarkers(d.State, d.Mode, d.Glyphs)) > 1
}
