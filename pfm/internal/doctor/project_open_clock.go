package doctor

import (
	"context"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

// openProjectTimer is the test seam of the clock the deadline runs on; nil is the real clock.
var openProjectTimer clock.Clock

// openProjectContext is the context the open-project rows are built under: it
// ends at openProjectDeadline on openProjectTimer, else on the real clock.
func openProjectContext() (context.Context, context.CancelFunc) {
	if openProjectTimer != nil {
		return clock.WithTimeout(context.Background(), openProjectTimer, openProjectDeadline)
	}
	return context.WithTimeout(context.Background(), openProjectDeadline)
}
