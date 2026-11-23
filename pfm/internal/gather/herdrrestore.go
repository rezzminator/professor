package gather

import (
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/herdr"
)

// HerdrRestorePending says a native seat whose supervisor is gone sits in a
// pane its Herdr server restored, the restored launch not started yet: the
// seat was last written before the server's current start, and that start
// lies inside the birth window. A socket that cannot be read is no restore,
// so the caller names its proof failure as before, never hides it.
func HerdrRestorePending(seat fleetdb.Seat) bool {
	start, err := herdr.ServerStart(seat.HerdrSocket)
	if err != nil {
		return false
	}
	started := time.Unix(0, start)
	return seat.UpdatedAt < started.Unix() && clock.Real.Now().Sub(started) < herdrBirthGrace
}
