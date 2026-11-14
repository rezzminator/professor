package spawn

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// RecordTmuxSeat registers a known engine session after a successful tmux birth.
// TmuxPane may be empty until the first identity hook learns its concrete pane.
func RecordTmuxSeat(ctx context.Context, values paths.Values, seat fleetdb.Seat) (returnErr error) {
	socket, err := values.SocketUnder(seat.TmuxSocket)
	if err != nil {
		return err
	}
	seat.TmuxSocket = socket
	seat.Backend = BackendTmux
	seat.State = "live"
	seat.UpdatedAt = clock.Real.Now().Unix()
	if seat.SeatID == "" {
		seat.SeatID = filepath.Base(socket)
	}
	state := fleetdb.OpenSharedState(ctx, values)
	defer func() { returnErr = errors.Join(returnErr, state.Close()) }()
	return state.PutSeat(ctx, seat)
}

func recordSpawnSeat(ctx context.Context, request Request) error {
	if request.SessionID == "" {
		return nil
	}
	values, err := paths.Resolve()
	if err != nil {
		return err
	}
	return RecordTmuxSeat(
		ctx,
		values,
		fleetdb.Seat{
			SeatID:     filepath.Base(request.Socket),
			SessionID:  request.SessionID,
			Engine:     string(request.Engine),
			Name:       request.Name,
			TmuxSocket: request.Socket,
		},
	)
}

// RetireTmuxSeat removes the tmux seat a chat server's socket names, from the shared state of this
// install (DeleteTmuxSeat).
func RetireTmuxSeat(ctx context.Context, socket string) (returnErr error) {
	values, err := paths.Resolve()
	if err != nil {
		return fmt.Errorf("resolve paths to retire tmux seat %s: %w", socket, err)
	}
	state := fleetdb.OpenSharedState(ctx, values)
	defer func() { returnErr = errors.Join(returnErr, state.Close()) }()
	return DeleteTmuxSeat(ctx, state, socket)
}

// DeleteTmuxSeat removes the tmux seat a server's socket names, as the
// engine's SessionEnd hook does (hookentry tmuxSeatLifecycle); a seat of
// another backend under that id stays.
func DeleteTmuxSeat(ctx context.Context, shared *fleetdb.Store, socket string) error {
	seat, found, err := shared.Seat(ctx, socket)
	if err != nil {
		return fmt.Errorf("read tmux seat %s: %w", socket, err)
	}
	if !found || seat.Backend != BackendTmux {
		return nil
	}
	return shared.DeleteSeat(ctx, socket)
}
