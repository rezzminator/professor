package doctor

import (
	"context"
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// Stale ownership is never permission to act, including after an unsupported downgrade.
func printHerdrSeatOwnership(ctx context.Context, out io.Writer, values paths.Values) int {
	seats, err := fleetdb.ReadSeats(ctx, values)
	if err != nil {
		fmt.Fprintf(out, "doctor: native ownership unreadable: %v\n", err)
		return 1
	}
	var native []fleetdb.Seat
	for index := range seats {
		seat := seats[index]
		if seat.Backend != "herdr" || seat.State != "live" {
			continue
		}
		if seat.SeatHostPID > 0 {
			dead, err := gather.HerdrHostDead(gather.NewProcFS(values.ProcRoot), seat)
			if err == nil && dead {
				fmt.Fprintf(
					out,
					"doctor: native ownership retired seat=%s: saved supervisor exited; repair: restore the intended pane with pfm chat open --restored %s;\n",
					seat.SeatID,
					fleetdb.ConversationIdentity(seat.SessionID, seat.SeatID),
				)
				continue
			}
			// Unknown birth evidence remains visible in the full diagnostic below.
		}
		native = append(native, seat)
	}
	warnings := 0
	rows := gather.ProbeHerdrSeats(ctx, values, native, true)
	for index := range rows {
		row := &rows[index]
		if row.Err != nil {
			warnings++
			fmt.Fprintf(
				out,
				"doctor: native ownership unproved seat=%s: %v; repair: restore the intended pane with pfm chat open --restored %s; do not retry stale pane actions\n",
				row.Seat.SeatID,
				row.Err,
				fleetdb.ConversationIdentity(row.Seat.SessionID, row.Seat.SeatID),
			)
		}
	}
	return warnings
}
