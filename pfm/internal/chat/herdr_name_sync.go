package chat

import (
	"context"
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/store"
)

// ConvergeHerdrNames carries in-engine /rename (which fires no title hook)
// from the indexed transcripts to proved native seats. name-sync calls this
// only on --apply, through the very same naming path as DeliverName.
func ConvergeHerdrNames(ctx context.Context, db *store.Store, values paths.Values, stderr io.Writer) (ok bool) {
	ok = true
	state := fleetdb.OpenSharedState(ctx, values)
	defer func() {
		if err := state.Close(); err != nil {
			fmt.Fprintf(stderr, "pfm name-sync: close native name state: %v\n", err)
			ok = false
		}
	}()
	seats, err := state.Seats(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "pfm name-sync: enumerate native names: %v\n", err)
		return false
	}
	for index := range seats {
		seat := &seats[index]
		if seat.Backend != spawn.BackendHerdr || seat.Engine != string(engine.Claude) || seat.State != "live" ||
			seat.EndPending {
			continue
		}
		indexed, found, err := db.Transcript(ctx, seat.SessionID)
		if err != nil {
			fmt.Fprintf(stderr, "pfm name-sync: read native name %s: %v\n", seat.SeatID, err)
			ok = false
			continue
		}
		if !found || indexed.CustomTitle == "" || indexed.CustomTitle == seat.Name {
			continue
		}
		target := headless.Chat{SeatID: seat.SeatID, ID: seat.SessionID, Socket: seat.HerdrSocket, Pane: seat.HerdrPane}
		if err := renameHerdr(ctx, target, indexed.CustomTitle, values); err != nil {
			fmt.Fprintf(
				stderr,
				"pfm name-sync: native seat %s NOT renamed %q: %v\n",
				seat.SeatID,
				indexed.CustomTitle,
				err,
			)
			ok = false
		}
	}
	return ok
}
