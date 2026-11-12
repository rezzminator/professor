package gather

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// herdrBirthGrace bounds a native birth between its reservation and the pane
// address it publishes: an unpublished reservation younger than this may still
// be in layout.apply, before its seat-host exists.
const herdrBirthGrace = 2 * time.Minute

// herdrSeatLive is the one durable state a scan may retire.
const herdrSeatLive = "live"

// retireDeadHerdrSeat writes the lost state pfm's restart retire writes, through
// UpdateSeat's compare-and-set on the row the proof read: a concurrent bind,
// restore or end changes that row and refuses this write. Only a live row
// retires; a seat with an end or reload in flight belongs to that lifecycle,
// and a reservation still inside its birth window has not yet had a pane to die in.
func retireDeadHerdrSeat(ctx context.Context, db *fleetdb.Store, seat fleetdb.Seat) (fleetdb.Seat, error) {
	if seat.State != herdrSeatLive || seat.EndPending || seat.ReloadPending {
		return seat, nil
	}
	if herdrBirthPending(seat) {
		return seat, nil
	}
	before := seat
	seat.State = "lost"
	seat.LostNote = "retired by pfm scan: saved supervisor exited; resume recipe retained"
	if err := db.UpdateSeat(ctx, before, seat); err != nil {
		return before, fmt.Errorf("retire dead seat %s: %w", seat.SeatID, err)
	}
	return seat, nil
}

// herdrBirthPending: a reservation inside its birth window that names no pane yet. SpawnHerdr reserves
// the row before layout.apply answers with the pane, and the seat-host that answer starts waits for it.
func herdrBirthPending(seat fleetdb.Seat) bool {
	return seat.HerdrPane == "" && clock.Real.Now().Sub(time.Unix(seat.UpdatedAt, 0)) < herdrBirthGrace
}

// herdrReservationErr judges a seat with no saved supervisor that Herdr lists no pane for: a reservation that
// names no pane yet is mid-birth inside its birth window (errHerdrSeatUnbound) and a failed birth past it
// (errHerdrBirthFailed); nil for a seat that once named a pane, which its pane's absence judges.
func herdrReservationErr(seat fleetdb.Seat) error {
	switch {
	case seat.HerdrPane != "":
		return nil
	case herdrBirthPending(seat):
		return fmt.Errorf("seat %s %w", seat.SeatID, errHerdrSeatUnbound)
	}
	return fmt.Errorf("seat %s %w", seat.SeatID, errHerdrBirthFailed)
}

// retireFailedHerdrBirth retires a reservation that named no pane within its birth window as the failed birth
// it is, the state a seat-host writes for its own failed birth, through UpdateSeat's compare-and-set on the
// row the proof read, and logs one WARN naming the seat. A read-only listing, or a seat whose end or reload is
// in flight, leaves the row as it is; a refused write is the listing's error.
func retireFailedHerdrBirth(ctx context.Context, db *fleetdb.Store, seat fleetdb.Seat, readOnly bool) error {
	if readOnly || seat.State != herdrSeatLive || seat.EndPending || seat.ReloadPending {
		return nil
	}
	before := seat
	seat.State = "failed"
	seat.LostNote = "retired by pfm scan: birth failed: no pane within the " + herdrBirthGrace.String() + " birth window"
	if err := db.UpdateSeat(ctx, before, seat); err != nil {
		return fmt.Errorf("HERDR ERROR %s: retire failed birth of seat %s: %w", seat.HerdrSocket, seat.SeatID, err)
	}
	obs.Logger(ctx).Warn("gather.herdr.birth_failed", obs.FieldSeat, seat.SeatID, "socket", seat.HerdrSocket,
		"note", seat.LostNote)
	return nil
}

// herdrSupervisorUnproved is a seat whose pane runs no seat-host of it: errHerdrSeatUnbound, a birth whose
// seat-host has not started yet, while the row changed inside the birth window; past it, a listing failure,
// since a chat whose supervisor is gone from its pane is never hidden as "not a chat yet".
func herdrSupervisorUnproved(seat fleetdb.Seat) error {
	if clock.Real.Now().Sub(time.Unix(seat.UpdatedAt, 0)) < herdrBirthGrace {
		return fmt.Errorf("seat %s %w", seat.SeatID, errHerdrSeatUnbound)
	}
	return fmt.Errorf("seat %s supervisor ownership unproved: its pane runs no seat-host past the %s birth window",
		seat.SeatID, herdrBirthGrace)
}

// errHerdrSeatGone marks a seat ended, replaced or moved off Herdr while it
// was read: no longer a live native seat of that read, and never its failure.
var errHerdrSeatGone = errors.New("ownership changed or disappeared")

// errHerdrSeatUnbound: a Herdr chat mid-birth — a reservation that names no pane
// yet, or whose pane shell runs no `pfm internal seat-host` yet.
var errHerdrSeatUnbound = errors.New("supervisor ownership unproved")

// errHerdrBirthFailed: a reservation that named no pane within its birth window, a birth that never finished.
var errHerdrBirthFailed = errors.New("birth failed: no pane within the birth window")

// herdrShellGone settles a proof whose pane answered with no shell: nil once
// the seat's saved supervisor is positively dead (the chat exited during this
// proof and Herdr still lists its closing pane), else the refusal. answered
// says the answer was for the seat's own pane.
func herdrShellGone(proc ProcFS, seat fleetdb.Seat, answered bool) error {
	unavailable := fmt.Errorf("seat %s ownership shell identity unavailable", seat.SeatID)
	if !answered || seat.SeatHostPID <= 0 || seat.SeatHostStart == 0 {
		return unavailable
	}
	dead, err := HerdrHostDead(proc, seat)
	if err != nil {
		return errors.Join(unavailable, err)
	}
	if dead {
		return nil
	}
	return unavailable
}

// errHerdrPaneAbsent: Herdr lists no pane holding the seat's pane or terminal.
var errHerdrPaneAbsent = errors.New("terminal not present in pane.list")

// currentHerdrSeat re-reads seat's durable row: errHerdrSeatGone when it is
// gone or no longer the same engine's native seat.
func currentHerdrSeat(ctx context.Context, db *fleetdb.Store, seat fleetdb.Seat) (fleetdb.Seat, error) {
	current, found, err := db.Seat(ctx, seat.SeatID)
	if err != nil {
		return seat, fmt.Errorf("read current ownership: %w", err)
	}
	if !found || current.Backend != nativeHerdrBackend || current.Engine != seat.Engine {
		return seat, fmt.Errorf("seat %s %w", seat.SeatID, errHerdrSeatGone)
	}
	return current, nil
}

// unansweredHerdrSeat judges a live seat whose Herdr socket did not answer
// from what needs no socket: its current row and its saved supervisor. Dead is
// that supervisor's positive death; a seat with no saved birth is never dead.
func unansweredHerdrSeat(
	ctx context.Context,
	db *fleetdb.Store,
	proc ProcFS,
	seat fleetdb.Seat,
) (fleetdb.Seat, bool, error) {
	current, err := currentHerdrSeat(ctx, db, seat)
	if err != nil {
		return seat, false, err
	}
	if current.SeatHostPID <= 0 || current.SeatHostStart == 0 {
		return current, false, nil
	}
	dead, err := HerdrHostDead(proc, current)
	return current, dead, err
}

// listDeadHerdrSeat lists a positively dead seat as lost, never live and never
// the recycled pane's occupant. A writable listing retires the durable row
// once, so later scans never probe it again.
func listDeadHerdrSeat(ctx context.Context, db *fleetdb.Store, row *HerdrSeat, canonical fleetdb.Seat, readOnly bool) {
	row.Err = fmt.Errorf("HERDR DEAD %s: saved supervisor exited", row.Seat.SeatID)
	if !readOnly {
		retired, retireErr := retireDeadHerdrSeat(ctx, db, canonical)
		row.Err = errors.Join(row.Err, retireErr)
		if retireErr == nil {
			row.Seat = retired
		}
	}
	row.Seat.State = "lost"
}

// settleHerdrProof proves seat and judges a failed proof against the seat's row as it stands after it: a
// row deleted or taken by another engine is errHerdrSeatGone, never the listing's failure; a row a
// concurrent writer changed (a birth stamping its supervisor, an end recording its intent) made the proof
// stale, so the current row is proved once more; an unchanged row keeps the failure.
func settleHerdrProof(
	ctx context.Context,
	db *fleetdb.Store,
	seat fleetdb.Seat,
	prove func(fleetdb.Seat) (fleetdb.Seat, bool, error),
) (fleetdb.Seat, bool, error) {
	canonical, owned, err := prove(seat)
	if err == nil || errors.Is(err, errHerdrSeatGone) {
		return canonical, owned, err
	}
	current, readErr := currentHerdrSeat(ctx, db, seat)
	switch {
	case errors.Is(readErr, errHerdrSeatGone):
		return seat, false, readErr
	case readErr != nil:
		return seat, false, errors.Join(err, readErr)
	case current == seat:
		return canonical, owned, err
	}
	return prove(current)
}

// listClosedHerdrSeat lists a seat whose pane Herdr no longer holds while its saved supervisor still runs
// (the pane was closed under it and the supervisor is on its way out) as lost for this listing, never as
// the listing's failure; the durable row retires once a later listing proves that supervisor dead.
func listClosedHerdrSeat(row *HerdrSeat, canonical fleetdb.Seat) {
	row.Err = fmt.Errorf(
		"HERDR DEAD %s: pane closed; saved supervisor %d has not exited", canonical.SeatID, canonical.SeatHostPID,
	)
	row.Seat.State = "lost"
}

// settleHerdrRow settles row's proof against its snapshot and, when the two disagree on the seat's pane,
// once more against a fresh listing of the socket: the proof reads the row and asks Herdr as they stand,
// both newer than the snapshot. A pane born since is absent from the snapshot, never from Herdr; a pane
// closed since is still in the snapshot, and Herdr refuses it.
func settleHerdrRow(
	ctx context.Context,
	db *fleetdb.Store,
	row *HerdrSeat,
	prove func(fleetdb.Seat) (fleetdb.Seat, bool, error),
) (fleetdb.Seat, bool, error) {
	canonical, owned, err := settleHerdrProof(ctx, db, row.Seat, prove)
	if !errors.Is(err, errHerdrPaneAbsent) && !herdr.PaneGone(err) {
		return canonical, owned, err
	}
	directories, snapshot, probeErr := probeHerdrPanes(ctx, row.Seat.HerdrSocket)
	if probeErr != nil {
		return canonical, owned, errors.Join(err, fmt.Errorf("re-read pane.list: %w", probeErr))
	}
	row.directories, row.snapshot = directories, snapshot
	return settleHerdrProof(ctx, db, row.Seat, prove)
}
