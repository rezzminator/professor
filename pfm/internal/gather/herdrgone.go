package gather

import (
	"context"
	"errors"
	"fmt"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/herdr"
)

// herdrHostExited is the death of a seat's saved supervisor, proved by its pid and kernel birth.
const herdrHostExited = "saved supervisor exited"

// herdrSocketGone is the death of a seat whose Herdr socket file is gone (herdr.SocketFileGone) and whose
// supervisor is positively dead or, never stamped, runs nowhere in the process table.
func herdrSocketGone(socket string) string {
	return "Herdr socket " + socket + " is gone and the seat's supervisor with it"
}

// unansweredHerdrSeat judges a live seat whose Herdr socket did not answer
// from what needs no socket: its current row and its saved supervisor. Dead is
// that supervisor's positive death. A seat with no saved birth is dead only
// when its socket file is gone (socketGone: herdr.SocketFileGone, never a refusal
// or a timeout) and no supervisor of it runs anywhere (HerdrSupervisorAbsent);
// a Herdr that is merely down never retires a chat whose supervisor runs.
// reason names the proof for the seat's loss note.
func unansweredHerdrSeat(
	ctx context.Context,
	db *fleetdb.Store,
	proc ProcFS,
	seat fleetdb.Seat,
	socketGone bool,
) (current fleetdb.Seat, dead bool, reason string, err error) {
	current, err = currentHerdrSeat(ctx, db, seat)
	if err != nil {
		return seat, false, "", err
	}
	reason = herdrHostExited
	if socketGone && current.HerdrSocket == seat.HerdrSocket {
		reason = herdrSocketGone(current.HerdrSocket)
	}
	if current.SeatHostPID <= 0 || current.SeatHostStart == 0 {
		if reason == herdrHostExited || herdrBirthPending(current) {
			return current, false, "", nil
		}
		dead, err = HerdrSupervisorAbsent(proc, current)
		return current, dead, reason, err
	}
	dead, err = HerdrHostDead(proc, current)
	return current, dead, reason, err
}

// listDeadHerdrSeat lists a positively dead seat as lost, never live and never
// the recycled pane's occupant, reason naming how its death was proved. A
// writable listing retires the durable row once, so later scans never probe it again.
func listDeadHerdrSeat(
	ctx context.Context,
	db *fleetdb.Store,
	row *HerdrSeat,
	canonical fleetdb.Seat,
	readOnly bool,
	reason string,
) {
	row.Err = fmt.Errorf("HERDR DEAD %s: %s", row.Seat.SeatID, reason)
	if !readOnly {
		retired, retireErr := retireDeadHerdrSeat(ctx, db, canonical, reason)
		row.Err = errors.Join(row.Err, retireErr)
		if retireErr == nil {
			row.Seat = retired
		}
	}
	row.Seat.State = herdrSeatLost
}

// unlistedHerdrSeat judges a proof whose pane.list failed with err: nil when the seat, never stamped,
// is dead because its socket file is gone and no supervisor of it runs (HerdrSeatRetired), else err.
func unlistedHerdrSeat(ctx context.Context, proc ProcFS, seat fleetdb.Seat, err error) error {
	if !herdr.SocketFileGone(err) || seat.SeatHostPID != 0 || herdrBirthPending(seat) {
		return err
	}
	retired, retireErr := HerdrSeatRetired(ctx, proc, seat)
	if retireErr == nil && retired {
		return nil
	}
	return errors.Join(err, retireErr)
}

// HerdrSeatRetired also handles pre-upgrade/never-bound rows. Missing process
// identity alone is unknown: require a complete absent listing, explicit alias
// refusal and no matching old supervisor anywhere in the process table; a
// socket file that is gone (herdr.SocketFileGone) stands in for the listing.
func HerdrSeatRetired(ctx context.Context, proc ProcFS, seat fleetdb.Seat) (bool, error) {
	if seat.SeatHostPID > 0 {
		return HerdrHostDead(proc, seat)
	}
	fail := func(err error) (bool, error) {
		return false, fmt.Errorf("seat %s has no saved supervisor birth; absence unproved: %w", seat.SeatID, err)
	}
	body, err := herdr.SeatRequest(ctx, seat.HerdrSocket, "pane.list", struct{}{})
	if herdr.SocketFileGone(err) && !herdrBirthPending(seat) {
		// No socket file is no Herdr server, so no pane: absence is the process table's alone.
		return HerdrSupervisorAbsent(proc, seat)
	}
	if err != nil {
		return fail(err)
	}
	panes, err := decodeHerdrPanes(body)
	if err != nil {
		return fail(err)
	}
	for index := range panes {
		pane := &panes[index]
		if pane.PaneID == seat.HerdrPane || (seat.HerdrTerminalID != "" && pane.TerminalID == seat.HerdrTerminalID) {
			return false, nil
		}
	}
	_, err = herdr.SeatRequest(ctx, seat.HerdrSocket, "pane.get", map[string]string{"pane_id": seat.HerdrPane})
	if !herdr.PaneGone(err) {
		return fail(errors.Join(err, errors.New("old pane absence not explicitly acknowledged")))
	}
	return HerdrSupervisorAbsent(proc, seat)
}
