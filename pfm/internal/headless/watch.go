package headless

import (
	"context"
	"fmt"
	"io"
	"time"

	"hostops/pfm/internal/clock"
)

// WatchOptions controls a blocking watch.
type WatchOptions struct {
	// IdleAfter is how long a chat must sit idle before IDLE is emitted. Zero
	// emits as soon as the transcript says the assistant has spoken.
	IdleAfter time.Duration
	// Poll is the sampling cadence.
	Poll time.Duration
	// OnIdle and OnExit run once, when their event fires.
	OnIdle func(Status) error
	OnExit func(Status) error
	// Once stops the watch after the first IDLE, instead of following the chat
	// until it dies.
	Once bool
}

// Watcher samples one chat. Resolve is re-run every tick rather than cached:
// a seat that dies must be SEEN to die, and a resolver that suddenly finds
// nothing is exactly that event.
type Watcher struct {
	Name    string
	Resolve func(context.Context) (Chat, bool, error)
	Now     func() time.Time
	Clock   clock.Clock
}

// Watch blocks, writing one line per event: IDLE when a chat stops owing an
// answer, EXIT when its server is gone, DEAD when it never existed. It returns
// the last status observed.
//
// The exit lines are the point of the command: a monitor that only ever hears
// about work would treat a crash as a long silence.
func (watcher Watcher) Watch(
	ctx context.Context,
	options WatchOptions,
	out io.Writer,
) (Status, error) {
	poll := options.Poll
	if poll <= 0 {
		poll = time.Second
	}
	now := watcher.Now
	if now == nil {
		if watcher.Clock == nil {
			watcher.Clock = clock.Real
		}
		now = watcher.Clock.Now
	} else if watcher.Clock == nil {
		watcher.Clock = clock.Real
	}
	announcedIdle := false
	for {
		if err := ctx.Err(); err != nil {
			return Status{}, err
		}
		chat, found, err := watcher.Resolve(ctx)
		if err != nil {
			return Status{}, err
		}
		if !found {
			status := Missing(watcher.Name)
			if _, err := fmt.Fprintf(out, "DEAD %s\n", watcher.Name); err != nil {
				return status, err
			}
			if options.OnExit != nil {
				if err := options.OnExit(status); err != nil {
					return status, err
				}
			}
			return status, nil
		}
		status, err := Inspect(ctx, chat, now())
		if err != nil {
			return status, err
		}
		if !status.Alive() {
			if _, err := fmt.Fprintf(out, "EXIT %s\n", watcher.Name); err != nil {
				return status, err
			}
			if options.OnExit != nil {
				if err := options.OnExit(status); err != nil {
					return status, err
				}
			}
			return status, nil
		}
		idleEnough := status.State == StateIdle &&
			time.Duration(status.IdleSeconds)*time.Second >= options.IdleAfter
		switch {
		case idleEnough && !announcedIdle:
			announcedIdle = true
			if _, err := fmt.Fprintf(
				out,
				"IDLE %s idle_seconds=%d\n",
				watcher.Name,
				status.IdleSeconds,
			); err != nil {
				return status, err
			}
			if options.OnIdle != nil {
				if err := options.OnIdle(status); err != nil {
					return status, err
				}
			}
			if options.Once {
				return status, nil
			}
		case status.State == StateWorking:
			// Back to work: the next idle is a new event worth announcing.
			announcedIdle = false
		}
		if err := waitForNextPoll(ctx, watcher.Clock, poll); err != nil {
			return status, err
		}
	}
}
