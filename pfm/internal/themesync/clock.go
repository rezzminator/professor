package themesync

import "github.com/rezzminator/professor/pfm/internal/clock"

// flipClock is the time seam of the latency measurement: read once after each
// surface's apply. Nil reads clock.Real at the call.
var flipClock clock.Clock

// clockOr is the clock a test set on a seam, else clock.Real read now: never a
// copy taken when the binary starts, so a process-wide fake reaches it.
func clockOr(override clock.Clock) clock.Clock {
	if override != nil {
		return override
	}
	return clock.Real
}
