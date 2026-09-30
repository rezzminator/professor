package inject

import pfmengine "github.com/rezzminator/professor/pfm/internal/engine"

// WaitingFor states the chained waiter's contract in its first log line.
// A Codex pane is named because an unobserved boundary leaves its steer
// undelivered (DeliverThen).
func WaitingFor(engineID string) string {
	what := "the current turn to end"
	if engineID == string(pfmengine.Codex) {
		return "engine codex · " + what +
			" — no steady-idle fallback: an unobserved boundary leaves the steer undelivered"
	}
	return what
}
