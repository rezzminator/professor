package hookentry

import (
	"context"
	"errors"
	"fmt"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
)

// bindNativeSeat writes seat over before. A new session in the seat (a fresh
// launch's provisional id replaced by the engine's, a /clear) keeps the space
// the seat's chat wore; a resumed one keeps its own (spawn.BirthSpace). A space
// that cannot carry is a warning: the binding already stands.
func (reporter *herdrReporter) bindNativeSeat(
	ctx context.Context,
	before, seat fleetdb.Seat,
	source string,
) (returnErr error) {
	writable := fleetdb.OpenSharedState(ctx, *reporter.deps.seatPaths)
	defer func() {
		if err := writable.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close native seat binding: %w", err))
		}
	}()
	if err := writable.UpdateSeat(ctx, before, seat); err != nil {
		return fmt.Errorf("bind native seat: %w", err)
	}
	if before.SessionID == "" || seat.SessionID == before.SessionID || source == herdrReasonResume {
		return nil
	}
	if _, err := writable.CarrySpace(ctx, before.SessionID, seat.SessionID, seat.UpdatedAt); err != nil {
		reporter.warn("pfm internal herdr-state %s: chat %s does not keep the space of %s: %v",
			reporter.options.event, seat.SessionID, before.SessionID, err)
	}
	return nil
}
