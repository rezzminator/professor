package chat

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

// ResumeRestoredSeat is the launcher's door for a `claude --resume ID` run in
// a Herdr pane, the line Herdr's own restore types into a pane it brought back:
// a native seat of that session whose supervisor is gone is rebound to this
// pane through OpenRestoredID, never left beside a second seat of the same
// conversation. handled is false when the session has no native seat or its
// seat is live, and when the seat could not be read, which it names on stderr.
func ResumeRestoredSeat(ctx context.Context, id string, stdout, stderr io.Writer, runtime config.Runtime) (
	handled bool,
	code int,
) {
	seat, found, err := nativeSeat(ctx, runtime, id, "")
	if err == nil && found {
		_, alive, aliveErr := nativeAlive(ctx, seat, runtime.Paths)
		if aliveErr == nil && alive {
			return false, 0
		}
		err = aliveErr
	}
	if err != nil {
		fmt.Fprintf(
			stderr,
			"pfm internal launch: could not read the native seat of session %s, so a new seat holds it: %v\n",
			id,
			err,
		)
		return false, 0
	}
	if !found {
		return false, 0
	}
	return true, OpenRestoredID(ctx, id, stdout, stderr, &runtime)
}

// restoredPaneConversation answers which conversation a restored open in a
// Herdr pane resumes, and the identity its lock takes. Herdr keeps the resume
// line pfm registered for a pane before it detected Claude there and ignores
// every later report or release (measured on Herdr 0.9.5), so the line it
// types back can name an older conversation of the pane; the live seat pfm
// holds for the pane is the current record and wins. Its supervisor is proved
// gone by restoreNative, which refuses a pane whose seat host still lives.
func restoredPaneConversation(
	ctx context.Context,
	runtime config.Runtime,
	choice spawn.BackendChoice,
	id string,
	seat fleetdb.Seat,
	found bool,
	stderr io.Writer,
) (string, string, error) {
	lockID := id
	if found {
		lockID = fleetdb.ConversationIdentity(seat.SessionID, seat.SeatID)
	}
	db := fleetdb.OpenSharedState(ctx, runtime.Paths)
	owner, owned, err := db.SeatByPane(ctx, choice.HerdrSocket, choice.HerdrPane)
	if err = errors.Join(err, db.Close()); err != nil {
		return id, lockID, fmt.Errorf("read the seat owning Herdr pane %s: %w", choice.HerdrPane, err)
	}
	if !owned || (found && owner.SeatID == seat.SeatID) {
		return id, lockID, nil
	}
	current := fleetdb.ConversationIdentity(owner.SessionID, owner.SeatID)
	fmt.Fprintf(
		stderr,
		"pfm chat open: Herdr pane %s belongs to seat %s, whose conversation is %s: restoring it instead of %s\n",
		choice.HerdrPane, owner.SeatID, current, id,
	)
	return current, current, nil
}
