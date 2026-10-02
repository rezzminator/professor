package reload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/inject"
)

type paneToldError struct{ cause error }

func (failure paneToldError) Error() string { return failure.cause.Error() }
func (failure paneToldError) Unwrap() error { return failure.cause }

// PaneTold reports that Run already displayed this failure on the pane.
func PaneTold(err error) bool {
	var told paneToldError
	return errors.As(err, &told)
}

// This file types /exit into the composer being rebooted: clear the composer
// first, prove the typed /exit rendered before Enter, and take it back out
// when the proof never comes. The pane READER it relies on is composer.go.

// stashDraft clears the composer before /exit is typed — and only when there
// is something to clear. Claude Code's Ctrl+S is a TOGGLE: it stashes a draft,
// but on an empty composer (a dim prompt suggestion is empty underneath) it
// RESTORES whatever stash is pending, and the /exit typed next lands on the end
// of that old draft — either never proven rendered, or submitted with it as a
// prompt. So an empty or hint-only composer gets no key at all, and a real
// draft must read empty after the key, or the reload is refused by name with
// nothing typed.
func stashDraft(
	ctx context.Context,
	request Request,
	clk clock.Clock,
	tmux Tmux,
	capture string,
	stderr io.Writer,
) error {
	empty, draft, err := composerEmpty(ctx, request, tmux, capture)
	if err != nil {
		return fmt.Errorf("%w — /exit was not typed", err)
	}
	if empty {
		return nil
	}
	if err := tmux.SendKey(ctx, request.SocketPath, request.Pane, "C-s"); err != nil {
		return fmt.Errorf("stash pane draft: %w", err)
	}
	state := "the composer was never read"
	for attempt := 0; attempt < 40; attempt++ {
		after, readErr := tmux.Capture(ctx, request.SocketPath, request.Pane)
		if readErr == nil {
			var left string
			empty, left, readErr = composerEmpty(ctx, request, tmux, after)
			if readErr == nil && empty {
				return nil
			}
			state = fmt.Sprintf("it shows %q", left)
		}
		if readErr != nil {
			state = fmt.Sprintf("the composer could not be read: %v", readErr)
			fmt.Fprintf(stderr, "pfm chat reload: confirm draft stashed (try %d): %v\n", attempt+1, readErr)
		}
		if err := clk.Sleep(ctx, 50*time.Millisecond); err != nil {
			return err
		}
	}
	cause := fmt.Errorf(
		"composer draft %q will not stash (after Ctrl+S %s) — /exit was not typed; clear or send the draft, then reload again",
		draft,
		state,
	)
	abort := "reload ABORTED — the composer holds a draft that will not stash; clear it, then reload again"
	if displayErr := tmux.Display(ctx, request.SocketPath, request.Pane, abort); displayErr != nil {
		return errors.Join(cause, fmt.Errorf("display stash refusal: %w", displayErr))
	}
	return paneToldError{cause}
}

// composerEmpty reports whether the composer holds no draft — no text after
// its marker, or only dim hint text — and returns the plain draft it read. A
// styled capture that fails is an error, never an empty composer.
func composerEmpty(ctx context.Context, request Request, tmux Tmux, capture string) (bool, string, error) {
	draft := composerDraftText(capture)
	if draft == "" {
		return true, "", nil
	}
	styled, err := tmux.CaptureStyled(ctx, request.SocketPath, request.Pane)
	if err != nil {
		return false, draft, fmt.Errorf("read composer styling (draft %q): %w", draft, err)
	}
	return inject.ComposerIsDimPlaceholder(styled), draft, nil
}

func waitExitRendered(ctx context.Context, request Request, clk clock.Clock, tmux Tmux, stderr io.Writer) error {
	seen := "the composer was never read"
	for attempt := 0; attempt < 40; attempt++ {
		capture, err := tmux.Capture(ctx, request.SocketPath, request.Pane)
		switch {
		case err != nil:
			seen = fmt.Sprintf("the composer could not be read: %v", err)
			fmt.Fprintf(stderr, "pfm chat reload: confirm typed /exit (try %d): %v\n", attempt+1, err)
		case composerShowsExit(capture):
			return nil
		default:
			seen = fmt.Sprintf("composer showed %q", composerDraftText(capture))
		}
		if err := clk.Sleep(ctx, 50*time.Millisecond); err != nil {
			return err
		}
	}
	cause := fmt.Errorf("typed /exit never rendered (%s) — refusing blind Enter", seen)
	if err := clearTypedExit(ctx, request, tmux); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

// clearTypedExit backspaces the "/exit" this worker itself just typed
// (SendLiteral, over a composer stashDraft had already proven empty) back out of
// the composer, so a refusal to reboot never leaves that stray text sitting
// there for a human to notice — or, worse, accidentally submit — then
// confirms the composer is empty again: any draft left, /exit or not, is named.
// Shared by waitExitRendered (never confirmed rendering, so pressing Enter
// would be blind) and exitIncomplete (rendered, then the pane never died on it).
func clearTypedExit(ctx context.Context, request Request, tmux Tmux) error {
	for range len("/exit") {
		if err := tmux.SendKey(ctx, request.SocketPath, request.Pane, "BSpace"); err != nil {
			return fmt.Errorf("the typed /exit could NOT be cleared from the composer — clear it by hand: %w", err)
		}
	}
	capture, err := tmux.Capture(ctx, request.SocketPath, request.Pane)
	if err != nil {
		return fmt.Errorf("sent backspaces over the typed /exit but could not confirm the composer is clear: %w", err)
	}
	empty, left, err := composerEmpty(ctx, request, tmux, capture)
	if err != nil {
		return fmt.Errorf("sent backspaces over the typed /exit but could not confirm the composer is clear: %w", err)
	}
	if !empty {
		return fmt.Errorf("after clearing the typed /exit the composer still shows %q — clear it by hand", left)
	}
	return nil
}
