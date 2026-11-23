package chat

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// RestartSeatHost is the seat host a Herdr seat names when `pfm restart`
// plans: the one the Herdr server stop ends, so its seat reads back only once
// the restored pane's resume has written another.
type RestartSeatHost struct {
	PID   int
	Start uint64
	Birth int64
}

// RestartRestorable says why chat's Herdr seat could not resume once `pfm
// restart` brings its Herdr server back, "" when it can, and the seat host
// the seat names now: the restored pane's resume (OpenRestoredID) needs the
// seat, no pending end and the seat's saved run recipe.
func RestartRestorable(ctx context.Context, chat headless.Chat, values paths.Values) (
	why string, host RestartSeatHost, returnErr error,
) {
	db := fleetdb.OpenSharedStateReadOnly(ctx, values)
	defer func() { returnErr = errors.Join(returnErr, db.Close()) }()
	seat, found, err := db.Seat(ctx, chat.SeatID)
	switch {
	case err != nil:
		return "", RestartSeatHost{}, fmt.Errorf("read seat %s: %w", chat.SeatID, err)
	case !found:
		return "its seat " + chat.SeatID + " is not in pfm's state", RestartSeatHost{}, nil
	}
	host = RestartSeatHost{PID: seat.SeatHostPID, Start: seat.SeatHostStart, Birth: seat.SeatHostBirth}
	if seat.EndPending {
		return "its seat " + chat.SeatID + " has an end pending", host, nil
	}
	_, err = db.ReadSeatRun(seat.SeatID)
	if errors.Is(err, fs.ErrNotExist) {
		return "its seat " + chat.SeatID + " has no saved run recipe to resume from", host, nil
	}
	if err != nil {
		return "", host, fmt.Errorf("read seat %s run recipe: %w", chat.SeatID, err)
	}
	return "", host, nil
}

// RestartBack reports whether chat is live again after `pfm restart` brought
// its Herdr server back, and what was seen: its seat reads live on socket,
// with no end pending, and names a running seat host other than before, the
// one it named at plan time: only the restored pane's resume writes another.
func RestartBack(ctx context.Context, chat headless.Chat, socket string, before RestartSeatHost, values paths.Values) (
	back bool, seen string, returnErr error,
) {
	db := fleetdb.OpenSharedStateReadOnly(ctx, values)
	defer func() { returnErr = errors.Join(returnErr, db.Close()) }()
	seat, found, err := db.Seat(ctx, chat.SeatID)
	switch {
	case err != nil:
		return false, "", fmt.Errorf("read seat %s: %w", chat.SeatID, err)
	case !found:
		return false, "seat " + chat.SeatID + " is gone from pfm's state", nil
	case seat.EndPending:
		return false, "seat " + chat.SeatID + " has an end pending", nil
	case seat.State != liveState:
		return false, "seat " + chat.SeatID + " reads " + seat.State, nil
	case seat.HerdrSocket != socket:
		return false, "seat " + chat.SeatID + " is on Herdr server " + seat.HerdrSocket + ", not " + socket, nil
	case seat.SeatHostPID <= 0:
		return false, "seat " + chat.SeatID + " has no seat host yet", nil
	case (RestartSeatHost{PID: seat.SeatHostPID, Start: seat.SeatHostStart, Birth: seat.SeatHostBirth}) == before:
		return false, fmt.Sprintf(
			"seat %s still names seat host pid %d from before the restart; no resume has started a new one",
			chat.SeatID,
			seat.SeatHostPID,
		), nil
	}
	dead, err := gather.HerdrHostDead(gather.NewProcFS(values.ProcRoot), seat)
	if err != nil {
		return false, "", fmt.Errorf("read seat %s seat host: %w", chat.SeatID, err)
	}
	if dead {
		return false, fmt.Sprintf(
			"seat %s still names seat host pid %d, which is gone; no resume has started a new one",
			chat.SeatID,
			seat.SeatHostPID,
		), nil
	}
	return true, fmt.Sprintf("seat %s live in Herdr pane %s, seat host pid %d",
		chat.SeatID, seat.HerdrPane, seat.SeatHostPID), nil
}
