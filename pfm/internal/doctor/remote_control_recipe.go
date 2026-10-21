package doctor

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/reload"
)

// Stored native recipes can outlive their account roster. Report these separately:
// an unattributed recipe cannot truthfully change any particular account's row.
// The read-only store never initializes, migrates or refreshes the inspected state.
func printRemoteControlRecipeDoctor(
	ctx context.Context,
	out io.Writer,
	values paths.Values,
	machine config.Config,
	tally *doctorTally,
) {
	db := fleetdb.OpenSharedStateReadOnly(ctx, values)
	defer func() {
		if err := db.Close(); err != nil {
			tally.fail()
			fmt.Fprintf(out, "doctor: remote_control recipes FAIL close read-only state: %v\n", err)
		}
	}()
	if err := db.Degraded(); err != nil {
		var mismatch *fleetdb.SchemaMismatchError
		switch {
		case errors.Is(err, fleetdb.ErrAbsent):
			fmt.Fprintln(out, "doctor: remote_control recipes checked=0 (state database absent)")
		case errors.As(err, &mismatch), errors.Is(err, paths.ErrLegacyPending):
			// Like the schedule audit, defer counting known unread state to
			// the shared-store row. Never migrate it or call its recipes clean.
			fmt.Fprintf(
				out,
				"doctor: remote_control recipes FAIL enumerate stored seats: %v; the doctor: shared store row counts it\n",
				err,
			)
		default:
			tally.fail()
			fmt.Fprintf(out, "doctor: remote_control recipes FAIL enumerate stored seats: %v\n", err)
		}
		return
	}
	seats, err := db.Seats(ctx)
	if err != nil {
		tally.fail()
		fmt.Fprintf(out, "doctor: remote_control recipes FAIL enumerate stored seats: %v\n", err)
		return
	}
	checked := 0
	for index := range seats {
		seat := &seats[index]
		if seat.Backend != "herdr" || seat.Engine != string(engine.Claude) {
			continue
		}
		checked++
		run, err := db.ReadSeatRun(seat.SeatID)
		if err != nil {
			// Decode errors can echo private recipe values. Report the operation
			// and immutable seat identity, never the recipe, env or raw JSON error.
			tally.fail()
			fmt.Fprintf(out,
				"doctor: remote_control recipe[%s] FAIL stored recipe unreadable or malformed; chat=%q\n",
				seat.SeatID, seat.Name)
			continue
		}
		if len(machine.Accounts) == 0 {
			continue // Rosterless launchers intentionally use machine preferences.
		}
		if _, found := reload.RecipeAccount(machine, engine.Claude, run.Env); !found {
			tally.warn()
			fmt.Fprintf(
				out,
				"doctor: remote_control recipe[%s] BLOCKED: account missing, ambiguous, or no longer configured; chat=%q; Remote Control not requested\n",
				seat.SeatID,
				seat.Name,
			)
		}
	}
	fmt.Fprintf(out, "doctor: remote_control recipes checked=%d native Claude stored recipes\n", checked)
}
