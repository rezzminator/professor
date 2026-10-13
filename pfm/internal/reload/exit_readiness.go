package reload

import (
	"context"
	"errors"
	"fmt"

	"github.com/rezzminator/professor/pfm/internal/inject"
)

// exitIncomplete names the state a refused /exit leaves behind, because
// "chat left running" on its own hid the one that mattered: an exit dialog
// nobody confirmed, or a typed /exit nobody submitted, sits in front of the
// composer and the harness holds every queued prompt — cron heartbeats,
// injected steers — behind it until a human clears it. Whatever this worker
// put on the screen, it takes back off, and the error says whether that was
// seen to succeed.
func exitIncomplete(ctx context.Context, request Request, tmux Tmux, polls int) error {
	cause := fmt.Errorf("/exit did not complete after %d polls; chat left running", polls)
	capture, err := tmux.Capture(ctx, request.SocketPath, request.Pane)
	if err != nil {
		return errors.Join(
			cause,
			fmt.Errorf(
				"could not read what the pane shows now — an exit dialog or the typed /exit may still be there, clear it by hand: %w",
				err,
			),
		)
	}
	switch {
	case inject.ExitDialogOpen(capture):
		if err := tmux.SendKey(ctx, request.SocketPath, request.Pane, "Escape"); err != nil {
			return errors.Join(
				cause,
				fmt.Errorf(
					"the exit dialog would not confirm and could NOT be dismissed — press Esc in the pane by hand: %w",
					err,
				),
			)
		}
		if capture, err = tmux.Capture(ctx, request.SocketPath, request.Pane); err != nil {
			return errors.Join(
				cause,
				fmt.Errorf("sent Esc to the exit dialog but could not confirm it closed: %w", err),
			)
		} else if inject.ExitDialogOpen(capture) {
			return errors.Join(
				cause,
				errors.New(
					"the exit dialog would not confirm and did not close on Esc — press Esc in the pane by hand",
				),
			)
		}
		return errors.Join(cause, errors.New("the exit dialog would not confirm; dismissed it (Esc)"))
	case composerShowsExit(capture):
		if err := clearTypedExit(ctx, request, tmux); err != nil {
			return errors.Join(cause, err)
		}
		return errors.Join(cause, errors.New("cleared the typed /exit from the composer"))
	case inject.LastComposerLine(capture) == "":
		return errors.Join(
			cause,
			errors.New(
				"the pane shows no composer, no typed /exit and no exit dialog — whether the chat took the /exit "+
					"is unknown; attach it and look",
			),
		)
	}
	return errors.Join(
		cause,
		fmt.Errorf(
			"the pane shows neither the typed /exit nor an exit dialog (composer shows %q) — the chat took the "+
				"/exit and is still ending; when it ends, its pane and server go with it, unrespawned",
			inject.ComposerDraftText(capture),
		),
	)
}
