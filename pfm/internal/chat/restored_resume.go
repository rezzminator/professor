package chat

import (
	"context"
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/config"
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
