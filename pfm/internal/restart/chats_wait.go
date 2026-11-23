package restart

import (
	"context"
	"errors"
	"fmt"
	"time"

	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/headless"
)

// chatWait is the one deadline every brought-back chat shares, and how the
// Herdr server start they all wait on went.
type chatWait struct {
	tools    serviceTools
	fleet    Fleet
	herdr    *herdrStarted
	deadline time.Time
}

// back polls until chat is live again on socket under a seat host other than
// before, within what is left of the shared deadline. A read that fails is
// kept as what was last seen and the poll goes on: the resume may still be
// writing the seat. A restored launch that failed (pfmchat.ErrRestartFailed)
// ends the wait at once, and a Herdr server that did not start again restores
// nothing, so no poll waits on either.
func (wait *chatWait) back(ctx context.Context, chat headless.Chat, socket string, before pfmchat.RestartSeatHost,
) (string, error) {
	if wait.herdr != nil && wait.herdr.err != nil {
		return "", fmt.Errorf("the Herdr server did not start again, so Herdr restored nothing; "+
			"reopen it by hand: pfm chat open %s", chat.ID)
	}
	if wait.deadline.IsZero() {
		wait.deadline = wait.tools.clock.Now().Add(chatBackWithin)
	}
	seen := "nothing read"
	up, err := wait.tools.waitFor(ctx, wait.deadline.Sub(wait.tools.clock.Now()), func() (bool, error) {
		back, detail, err := wait.fleet.Back(ctx, chat, socket, before)
		if errors.Is(err, pfmchat.ErrRestartFailed) {
			return false, err
		}
		if err != nil {
			seen = err.Error()
			return false, nil
		}
		seen = detail
		return back, nil
	})
	if errors.Is(err, pfmchat.ErrRestartFailed) {
		return "", fmt.Errorf("%w; reopen it by hand: pfm chat open %s", err, chat.ID)
	}
	if err != nil {
		return "", err
	}
	if !up {
		return "", fmt.Errorf("not live again within %s of the Herdr server start (last seen: %s); "+
			"reopen it by hand: pfm chat open %s", chatBackWithin, seen, chat.ID)
	}
	return "live again: " + seen, nil
}

// herdrStarted is how the restart's Herdr server start went: its error once it failed.
type herdrStarted struct {
	err error
}

// track wraps the Herdr server's start so every brought-back chat's start,
// which runs after it, reads how it went.
func (started *herdrStarted) track(start func(context.Context) (string, error)) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		detail, err := start(ctx)
		started.err = err
		return detail, err
	}
}
