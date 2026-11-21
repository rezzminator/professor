package hookentry

import (
	"context"
	"fmt"
	"io"
	"syscall"
	"time"

	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/herdr"
)

// seatEngineEnded decides what an engine's end does to seat, whose binding is
// this supervisor's: a seat awaiting a reload after a clean run restarts with
// next, anything else closes with closeSeat's code.
func seatEngineEnded(
	ctx context.Context,
	db *fleetdb.Store,
	seat fleetdb.Seat,
	status int,
	faulted bool,
	stderr io.Writer,
	runtime config.Runtime,
) (next fleetdb.Seat, reloading bool, code int) {
	next, found, err := db.Seat(ctx, seat.SeatID)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal seat-host: read ended seat: %v\n", err)
		return seat, false, 1
	}
	if found && next.ReloadPending && !faulted {
		return next, true, 0
	}
	return seat, false, closeSeat(ctx, db, seat, status, stderr, runtime)
}

// closeSeat keeps the supervisor alive while an independent worker closes the
// pane. HUP is restoration, not end; an already pending worker owns cleanup.
func closeSeat(
	ctx context.Context,
	db *fleetdb.Store,
	seat fleetdb.Seat,
	status int,
	stderr io.Writer,
	runtimes ...config.Runtime,
) int {
	if status == 128+int(syscall.SIGHUP) {
		return status
	}
	current, found, err := db.Seat(ctx, seat.SeatID)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal seat-host: read ended seat: %v\n", err)
		return 1
	}
	if !found {
		return status
	}
	if current.SeatHostPID != seat.SeatHostPID || current.SeatHostStart != seat.SeatHostStart ||
		current.SeatHostBirth != seat.SeatHostBirth {
		fmt.Fprintln(stderr, "pfm internal seat-host: ended supervisor binding changed")
		return 1
	}
	runtime, err := config.OptionalRuntime(runtimes)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal seat-host: end runtime: %v\n", err)
		return 1
	}
	if !current.EndPending {
		if err := pfmchat.ScheduleHerdrEnd(
			ctx,
			headless.Chat{Backend: current.Backend, SeatID: current.SeatID, ID: current.SessionID},
			runtime,
		); err != nil {
			if herdr.ServerStopping(err) {
				// Herdr is stopping and takes this pane with it; macOS ends the engine before
				// the terminal hangs up. That is restoration, as the hangup is.
				return 128 + int(syscall.SIGHUP)
			}
			fmt.Fprintf(stderr, "pfm internal seat-host: schedule ended seat: %v\n", err)
			return 1
		}
	}
	deadline := clock.Real.NewTimer(10 * time.Second)
	defer deadline.Stop()
	tick := clock.Real.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			fmt.Fprintf(stderr, "pfm internal seat-host: wait for retirement: %v\n", ctx.Err())
			return 1
		case <-deadline.C():
			fmt.Fprintln(stderr, "pfm internal seat-host: retirement unconfirmed; pending owner retained")
			return 1
		case <-tick.C():
			_, found, err := db.Seat(ctx, seat.SeatID)
			if err != nil {
				fmt.Fprintf(stderr, "pfm internal seat-host: retirement state: %v\n", err)
				return 1
			}
			if !found {
				return status
			}
		}
	}
}

// failedSeatStart preserves the durable row and recipe for diagnosis. It is not
// the normal engine-exit path: the pane remains available to show the failure.
func failedSeatStart(
	ctx context.Context,
	db *fleetdb.Store,
	seat fleetdb.Seat,
	cause error,
	stderr io.Writer,
) int {
	fmt.Fprintf(stderr, "pfm internal seat-host: seat %s: %v\n", seat.SeatID, cause)
	// Reload retains conversation exclusion while awaiting this report. Publish
	// through UpdateSeat's binding-guarded lifecycle CAS, never wait on its lock.
	current, found, err := db.Seat(ctx, seat.SeatID)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal seat-host: read failed seat: %v\n", err)
		return 1
	}
	if !found || current.EndPending || current.Backend != seat.Backend || current.HerdrSocket != seat.HerdrSocket ||
		current.HerdrTerminalID != seat.HerdrTerminalID || current.Engine != seat.Engine ||
		current.SeatHostPID != seat.SeatHostPID || current.SeatHostStart != seat.SeatHostStart || current.SeatHostBirth != seat.SeatHostBirth {
		fmt.Fprintln(stderr, "pfm internal seat-host: failed supervisor binding changed")
		return 1
	}
	seat = current
	before := seat
	seat.State = "failed"
	seat.LostNote = cause.Error()
	seat.UpdatedAt = clock.Real.Now().Unix()
	if err := db.UpdateSeat(ctx, before, seat); err != nil {
		fmt.Fprintf(stderr, "pfm internal seat-host: record failed seat %s: %v\n", seat.SeatID, err)
	}
	return 1
}
