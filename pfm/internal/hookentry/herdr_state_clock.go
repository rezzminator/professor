package hookentry

import (
	"context"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

// clock is the clock this reporter's waits cross.
func (reporter *herdrReporter) clock() clock.Clock {
	if reporter.deps.clock != nil {
		return reporter.deps.clock
	}
	return clock.Real
}

// deadlineContext is the context every reporter transaction runs under: it
// ends herdrDeadline after this call, on the reporter's clock.
func (reporter *herdrReporter) deadlineContext() (context.Context, context.CancelFunc) {
	return clock.WithTimeout(context.Background(), reporter.clock(), herdrDeadline)
}
