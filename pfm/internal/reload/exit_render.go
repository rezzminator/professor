package reload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
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
// when the proof never comes. Both workers — tmux's Run and Herdr's RunHerdr —
// run these same steps; the host is only the seatScreen they hand in. The pane
// READER it relies on is composer.go.

// exitRenderReads bound the wait for a typed /exit to show in the composer,
// exitRenderPoll apart, on either host.
const (
	exitRenderReads = 40
	exitRenderPoll  = 50 * time.Millisecond
)

// seatScreen is the rebooted chat's screen: call's one Screen door, bound to
// the target its worker decided once. bind re-proves that target before every
// read and key — a Herdr seat's supervisor can be replaced under the worker; a
// tmux pane's address cannot. tell shows a refusal on the pane itself; it is
// nil where the host shows a worker's failure another way (Herdr: the worker's
// notification).
type seatScreen struct {
	door pfmcall.Screen
	bind func(context.Context) (pfmcall.Target, error)
	tell func(context.Context, string) error
}

// tmuxPane is the tmux worker's seatScreen: a fixed pane address, and a
// refusal displayed on that pane.
func tmuxPane(request Request, tmux Tmux) seatScreen {
	target := request.target()
	return seatScreen{
		door: tmux,
		bind: func(context.Context) (pfmcall.Target, error) { return target, nil },
		tell: func(ctx context.Context, message string) error {
			return tmux.Display(ctx, request.SocketPath, request.Pane, message)
		},
	}
}

// capture reads the bound target's visible frame. A seat that moved under
// the read is followed once: that frame belongs to neither address.
func (pane seatScreen) capture(ctx context.Context) (string, error) {
	target, err := pane.bind(ctx)
	if err != nil {
		return "", err
	}
	for range 2 {
		screen, readErr := pane.door.CaptureScreen(ctx, target)
		after, err := pane.bind(ctx)
		if err != nil {
			return "", err
		}
		if after == target {
			return screen, readErr
		}
		target = after
	}
	return "", fmt.Errorf("pane %s moved under every read of it", target.Pane)
}

// styled is pfmcall.ComposerEmpty's styled read of this pane: asked only when
// the plain read shows a draft, to tell a dim suggestion from text.
func (pane seatScreen) styled(ctx context.Context) func() (string, error) {
	return func() (string, error) {
		target, err := pane.bind(ctx)
		if err != nil {
			return "", err
		}
		return pane.door.CaptureStyled(ctx, target)
	}
}

func (pane seatScreen) key(ctx context.Context, key string) error {
	target, err := pane.bind(ctx)
	if err != nil {
		return err
	}
	return pane.door.SendKey(ctx, target, key)
}

func (pane seatScreen) literal(ctx context.Context, text string) error {
	target, err := pane.bind(ctx)
	if err != nil {
		return err
	}
	return pane.door.SendLiteral(ctx, target, text)
}

// refuse is every refusal's failure path: shown on the pane where the host
// can (paneToldError then keeps the worker from announcing it a second time),
// returned as it stands where it cannot.
func (pane seatScreen) refuse(ctx context.Context, cause error, what, abort string) error {
	if pane.tell == nil {
		return cause
	}
	if err := pane.tell(ctx, abort); err != nil {
		return errors.Join(cause, fmt.Errorf("display %s refusal: %w", what, err))
	}
	return paneToldError{cause}
}

// refuseSelector refuses a reboot while a selector menu or the trust dialog
// stands in front of the composer: a /exit typed there answers the menu.
func refuseSelector(ctx context.Context, pane seatScreen, screen string) error {
	if !selectorOpen(screen) && !pfmengine.ClaudeTrustDialog(screen) {
		return nil
	}
	return pane.refuse(
		ctx,
		errors.New("open selector menu on the pane — refusing to /exit"),
		"selector",
		"reload ABORTED — answer the open menu first, then reload again",
	)
}

// stashDraft clears the composer before /exit is typed — and only when there
// is something to clear. Claude Code's Ctrl+S is a TOGGLE: it stashes a draft,
// but on an empty composer (a dim prompt suggestion is empty underneath) it
// RESTORES whatever stash is pending, and the /exit typed next lands on the end
// of that old draft — either never proven rendered, or submitted with it as a
// prompt. So an empty or hint-only composer gets no key at all, and a real
// draft must read empty after the key, or the reload is refused by name with
// nothing typed.
func stashDraft(ctx context.Context, pane seatScreen, clk clock.Clock, capture string, stderr io.Writer) error {
	empty, draft, err := pfmcall.ComposerEmpty(capture, pane.styled(ctx))
	if err != nil {
		return fmt.Errorf("%w — /exit was not typed", err)
	}
	if empty {
		return nil
	}
	if err := pane.key(ctx, "C-s"); err != nil {
		return fmt.Errorf("stash pane draft: %w", err)
	}
	state := "the composer was never read"
	for attempt := 0; attempt < exitRenderReads; attempt++ {
		after, readErr := pane.capture(ctx)
		if readErr == nil {
			var left string
			empty, left, readErr = pfmcall.ComposerEmpty(after, pane.styled(ctx))
			if readErr == nil && empty {
				return nil
			}
			state = fmt.Sprintf("it shows %q", left)
		}
		if readErr != nil {
			state = fmt.Sprintf("the composer could not be read: %v", readErr)
			fmt.Fprintf(stderr, "pfm chat reload: confirm draft stashed (try %d): %v\n", attempt+1, readErr)
		}
		if err := clk.Sleep(ctx, exitRenderPoll); err != nil {
			return err
		}
	}
	return pane.refuse(
		ctx,
		fmt.Errorf(
			"composer draft %q will not stash (after Ctrl+S %s) — /exit was not typed; clear or send the draft, then reload again",
			draft,
			state,
		),
		"stash",
		"reload ABORTED — the composer holds a draft that will not stash; clear it, then reload again",
	)
}

// typeExit types /exit into a composer its worker proved idle and empty,
// proves it rendered, and presses Enter. A /exit that never renders is
// backspaced back out before the refusal returns. entered reports that Enter
// was attempted: past it the engine may already be exiting, even when the
// key's acknowledgement was lost.
func typeExit(ctx context.Context, pane seatScreen, clk clock.Clock, stderr io.Writer) (entered bool, err error) {
	if err := pane.literal(ctx, "/exit"); err != nil {
		return false, fmt.Errorf("send /exit: %w", err)
	}
	if err := waitExitRendered(ctx, pane, clk, stderr); err != nil {
		return false, err
	}
	return pressEnter(ctx, pane, "submit /exit")
}

// pressEnter submits what the composer holds. entered turns true only once the
// target re-proved and the key went out: a target lost before it is a refusal
// with nothing pressed.
func pressEnter(ctx context.Context, pane seatScreen, what string) (entered bool, err error) {
	target, err := pane.bind(ctx)
	if err != nil {
		return false, err
	}
	if err := pane.door.SendKey(ctx, target, "Enter"); err != nil {
		return true, fmt.Errorf("%s: %w", what, err)
	}
	return true, nil
}

func waitExitRendered(ctx context.Context, pane seatScreen, clk clock.Clock, stderr io.Writer) error {
	seen := "the composer was never read"
	for attempt := 0; attempt < exitRenderReads; attempt++ {
		capture, err := pane.capture(ctx)
		switch {
		case err != nil:
			seen = fmt.Sprintf("the composer could not be read: %v", err)
			fmt.Fprintf(stderr, "pfm chat reload: confirm typed /exit (try %d): %v\n", attempt+1, err)
		case composerShowsExit(capture):
			return nil
		default:
			seen = fmt.Sprintf("composer showed %q", pfmcall.ComposerDraftText(capture))
		}
		if err := clk.Sleep(ctx, exitRenderPoll); err != nil {
			return err
		}
	}
	cause := fmt.Errorf("typed /exit never rendered (%s) — refusing blind Enter", seen)
	if err := clearTypedExit(ctx, pane); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

// clearTypedExit backspaces the "/exit" this worker itself just typed (over a
// composer already proven empty) back out of the composer, so a refusal to
// reboot never leaves that stray text sitting there for a human to notice —
// or, worse, accidentally submit — then confirms the composer is empty again:
// any draft left, /exit or not, is named. Shared by waitExitRendered (never
// confirmed rendering, so pressing Enter would be blind), exitIncomplete
// (rendered, then the pane never died on it) and the Herdr rollback.
func clearTypedExit(ctx context.Context, pane seatScreen) error {
	for range len("/exit") {
		if err := pane.key(ctx, "BSpace"); err != nil {
			return fmt.Errorf("the typed /exit could NOT be cleared from the composer — clear it by hand: %w", err)
		}
	}
	capture, err := pane.capture(ctx)
	if err != nil {
		return fmt.Errorf("sent backspaces over the typed /exit but could not confirm the composer is clear: %w", err)
	}
	empty, left, err := pfmcall.ComposerEmpty(capture, pane.styled(ctx))
	if err != nil {
		return fmt.Errorf("sent backspaces over the typed /exit but could not confirm the composer is clear: %w", err)
	}
	if !empty {
		return fmt.Errorf("after clearing the typed /exit the composer still shows %q — clear it by hand", left)
	}
	return nil
}
