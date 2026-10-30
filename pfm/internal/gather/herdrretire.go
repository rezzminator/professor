package gather

import (
	"context"
	"fmt"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
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
	if seat.HerdrPane == "" && clock.Real.Now().Sub(time.Unix(seat.UpdatedAt, 0)) < herdrBirthGrace {
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
