package spawn

import (
	"context"
	"errors"
	"path/filepath"

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
