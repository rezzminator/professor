package reload

import (
	"context"
	"fmt"
	"io"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
)

// carryReminders moves the reminders of the session a reboot left behind onto
// the session the pane runs now: a reminder belongs to the chat, and a --new
// reboot, or a queued reload continuing one, carries the chat on under a new
// session id. A resume keeps its id, so nothing moves. The reboot has already
// happened when this runs, so a failed move does not undo it: it is told on
// stderr with both ids and its cause, never swallowed.
func carryReminders(ctx context.Context, leftBehind, sessionID string, stderr io.Writer) {
	if leftBehind == "" || leftBehind == sessionID {
		return
	}
	target := sessionID
	if target == "" {
		target = "the new conversation"
	}
	failed := func(err error) {
		fmt.Fprintf(stderr,
			"pfm chat reload: the reminders of session %s did NOT move to %s: %v — "+
				"check pfm chat reminder ls and set them again on this chat\n",
			leftBehind, target, err)
	}
	values, err := pfmconfig.ResolvePaths()
	if err != nil {
		failed(fmt.Errorf("resolve state paths: %w", err))
		return
	}
	state := fleetdb.OpenSharedState(ctx, values)
	defer func() {
		if err := state.Close(); err != nil {
			fmt.Fprintf(stderr, "pfm chat reload: close the reminder store after moving %s: %v\n", leftBehind, err)
		}
	}()
	if err := state.Degraded(); err != nil {
		failed(err)
		return
	}
	if sessionID != "" {
		if _, err := state.RekeyReminders(ctx, leftBehind, sessionID); err != nil {
			failed(err)
		}
		return
	}
	// A Codex --new conversation has no id until it answers: nothing to move
	// to, so the reminders stay on the conversation left behind, told.
	reminders, err := state.Reminders(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat reload: read the reminders of session %s: %v\n", leftBehind, err)
		return
	}
	stayed := 0
	for i := range reminders {
		if reminders[i].SessionID == leftBehind {
			stayed++
		}
	}
	if stayed > 0 {
		fmt.Fprintf(stderr,
			"pfm chat reload: %d reminder(s) of session %s stay on the conversation left behind — "+
				"the new conversation has no id yet; set them again on this chat once it has answered\n",
			stayed, leftBehind)
	}
}
