package chat

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

// SeatStartWindow is how long an engine start has to prove itself: a resumed
// engine that exits non-zero inside it failed its start (the seat host keeps
// the seat, its recipe and the pane's error), and pfm restart reads a
// restored chat back only once its seat host has lived this long.
const SeatStartWindow = 10 * time.Second

// ErrRestartFailed marks a restored chat pfm restart need not wait for: its
// restored launch failed, or its restore was refused, and nothing retries it.
var ErrRestartFailed = errors.New("its restored launch failed")

// seatFailedState is the state failedSeatStart records: a start that failed,
// its seat and recipe kept, its cause the seat's loss note.
const seatFailedState = "failed"

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
// one it named at plan time: only the restored pane's resume writes another;
// that seat host has outlived SeatStartWindow by timing. An error wrapping
// ErrRestartFailed is a seat that will not come back: its restored launch
// failed or its restore was refused (it reads failed), or the restored seat
// host is ending it. A seat absent or not yet rebound is only "not yet".
func RestartBack(
	ctx context.Context,
	chat headless.Chat,
	socket string,
	before RestartSeatHost,
	values paths.Values,
	timing clock.Clock,
) (
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
	case seat.State == seatFailedState && failedSinceServerStart(seat, socket):
		return false, "", fmt.Errorf("%w: seat %s: %s", ErrRestartFailed, chat.SeatID, seat.LostNote)
	case seat.State == seatFailedState:
		return false, "seat " + chat.SeatID + " reads failed from before the Herdr server start", nil
	case seat.EndPending && seat.SeatHostPID > 0 &&
		(RestartSeatHost{PID: seat.SeatHostPID, Start: seat.SeatHostStart, Birth: seat.SeatHostBirth}) != before:
		// Only an end of the restored launch's own seat host is pending here:
		// the plan left no seat with an end pending.
		return false, "", fmt.Errorf("%w: seat %s is ending: its restored engine under seat host pid %d exited",
			ErrRestartFailed, chat.SeatID, seat.SeatHostPID)
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
	// A restored engine that dies at start went live first: back is read only
	// once its seat host has outlived the start window (a seat with no birth
	// stamp, a legacy one, is read back at once).
	if lived := time.Duration(timing.Now().Unix()-seat.SeatHostBirth) * time.Second; seat.SeatHostBirth > 0 &&
		lived < SeatStartWindow {
		return false, fmt.Sprintf(
			"seat %s live in Herdr pane %s under seat host pid %d for %s, inside its %s start window",
			chat.SeatID,
			seat.HerdrPane,
			seat.SeatHostPID,
			lived,
			SeatStartWindow,
		), nil
	}
	return true, fmt.Sprintf("seat %s live in Herdr pane %s, seat host pid %d",
		chat.SeatID, seat.HerdrPane, seat.SeatHostPID), nil
}

// restoreRefused records refused, the failure of a restored open run in the
// seat's own pane, on a live seat (one pfm restart left) or one the open
// already rebound (lost) whose seat host or legacy supervisor is proved gone:
// the seat reads failed, the refusal before its loss note. Herdr runs a pane's
// resume once, so nothing else brings that chat back, and pfm restart reads it
// at once. An open from another pane, a living owner, an unproved absence or
// any other state leaves the seat as it is. refused comes back as it was,
// joined with a failure to read or write the seat.
func restoreRefused(
	ctx context.Context,
	runtime config.Runtime,
	seat fleetdb.Seat,
	found bool,
	choice spawn.BackendChoice,
	refused error,
) error {
	if refused == nil || !found {
		return refused
	}
	db := fleetdb.OpenSharedState(ctx, runtime.Paths)
	current, stored, err := db.Seat(ctx, seat.SeatID)
	if err == nil && stored && (current.State == liveState || current.State == stateLost) && !current.EndPending &&
		current.HerdrSocket == choice.HerdrSocket && current.HerdrPane == choice.HerdrPane {
		// The pane is the open's own, present by design: only the process table proves the owner gone.
		proc := gather.NewProcFS(runtime.Paths.ProcRoot)
		dead, proofErr := gather.HerdrSupervisorAbsent(proc, current)
		if current.SeatHostPID > 0 {
			dead, proofErr = gather.HerdrHostDead(proc, current)
		}
		if proofErr == nil && dead {
			failed := current
			failed.State = seatFailedState
			failed.LostNote = strings.Trim("restore refused: "+refused.Error()+"; "+current.LostNote, "; ")
			failed.UpdatedAt = clock.Real.Now().Unix()
			err = db.UpdateSeat(ctx, current, failed)
		}
	}
	if err = errors.Join(err, db.Close()); err != nil {
		return errors.Join(refused, fmt.Errorf("record the refused restore on seat %s: %w", seat.SeatID, err))
	}
	return refused
}

// failedSinceServerStart says seat was marked failed at or after the current
// start of the Herdr server at socket (herdr.ServerStart, its socket's mtime):
// a failure of the restored launch, never one the seat carried into the
// restart. A socket that cannot be read counts as since: no server answers
// there, so nothing restores the chat.
func failedSinceServerStart(seat fleetdb.Seat, socket string) bool {
	start, err := herdr.ServerStart(socket)
	return err != nil || seat.UpdatedAt >= time.Unix(0, start).Unix()
}
