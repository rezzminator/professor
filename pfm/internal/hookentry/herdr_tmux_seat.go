package hookentry

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/reload"
)

// tmuxSeatLifecycle updates only this launcher's durable row; it does not
// change tmux bridge state, hooks, target selection or attach choreography.
func (reporter *herdrReporter) tmuxSeatLifecycle(ctx context.Context, payload herdrPayload) (returnErr error) {
	if payload.SessionID == "" ||
		(reporter.options.event != herdr.EventStart && reporter.options.event != herdr.EventEnd) {
		return nil
	}
	socket := reporter.deps.identity().TmuxSocket()
	db := fleetdb.OpenSharedState(ctx, *reporter.deps.seatPaths)
	defer func() {
		if err := db.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close tmux seat state: %w", err))
		}
	}()
	seat, found, err := db.Seat(ctx, filepath.Base(socket))
	if err != nil {
		return fmt.Errorf("read tmux seat: %w", err)
	}
	if !found || seat.Backend != "tmux" {
		return nil
	}
	if reporter.options.event == herdr.EventEnd {
		if payload.Reason == herdrReasonClear || payload.Reason == herdrReasonResume || seat.ReloadPending {
			return nil
		}
		// A reload's own /exit ends the engine it respawns on this seat: while it holds the pane the
		// seat stays, else the reloaded chat runs with no seat.
		inFlight, err := reload.InFlight(reporter.deps.sidDir, filepath.Base(socket), reporter.tmuxPane)
		if err != nil {
			return fmt.Errorf("keep tmux seat %s through a reload: %w", seat.SeatID, err)
		}
		if inFlight {
			return nil
		}
		return db.DeleteSeat(ctx, seat.SeatID)
	}
	before := seat
	seat.SessionID = payload.SessionID
	return db.UpdateSeat(ctx, before, seat)
}
