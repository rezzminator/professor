package picker

import (
	"time"

	"github.com/rezzminator/professor/pfm/internal/ui"
)

// refreshCadence is one refresh stream's backoff state. It starts at
// fleetRefreshInterval and stretches by fleetRefreshGrowth after each pass
// that nobody interrupted, capped at fleetRefreshParkThreshold, so a picker
// being driven stays prompt. streamFleetRefreshesWith is what turns
// "capped" into "stopped" — see the park/poll split there.
type refreshCadence struct {
	activity  *ui.ActivityClock
	lastStamp int64
	interval  time.Duration
}

func newRefreshCadence(activity *ui.ActivityClock) *refreshCadence {
	return &refreshCadence{
		activity:  activity,
		lastStamp: activity.StampNS(),
		interval:  fleetRefreshInterval,
	}
}

// next reports how long to wait before the next pass. Any interaction since
// the previous call snaps the cadence back to fleetRefreshInterval; otherwise
// it grows, capped at fleetRefreshMaxInterval.
//
// A nil clock — every non-interactive caller — never backs off. An absent
// presence signal is a claim about US, not about the user, and must never be
// spent as evidence that nobody is there.
func (cadence *refreshCadence) next() time.Duration {
	if cadence.activity == nil {
		return fleetRefreshInterval
	}
	if stamp := cadence.activity.StampNS(); stamp != cadence.lastStamp {
		cadence.lastStamp = stamp
		cadence.interval = fleetRefreshInterval
		return cadence.interval
	}
	grown := time.Duration(float64(cadence.interval) * fleetRefreshGrowth)
	if grown > fleetRefreshParkThreshold {
		grown = fleetRefreshParkThreshold
	}
	cadence.interval = grown
	return cadence.interval
}

// failureBackoff is the wait before the retry that follows the streak-th
// consecutive failed pass: fleetRefreshInterval after the first, doubling with
// each further failure, capped at fleetRefreshParkThreshold. A refresh that
// keeps failing (tmux that cannot run, an index that cannot open) costs a full
// pass per retry, so it must not retry on every park poll.
func failureBackoff(streak int) time.Duration {
	wait := fleetRefreshInterval
	for step := 1; step < streak && wait < fleetRefreshParkThreshold; step++ {
		wait *= 2
	}
	return min(wait, fleetRefreshParkThreshold)
}
