package chat

import (
	"context"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

// steppedClock is the clock of a loop whose every pause is the gap between two looks at things
// already there (the launch watch reading a fake tmux's pane): each Sleep ends at once, the
// fake's time moved on by the pause, so the watch's quiet and deadline bounds count pauses and
// never host speed. A pause the context ends first reports its error, as on the real clock.
type steppedClock struct{ *clock.Fake }

func (stepped steppedClock) Sleep(ctx context.Context, pause time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	stepped.Advance(pause)
	return nil
}

// newSteppedClock starts at the host's wall time: a deadline built on it reads true to the kernel.
func newSteppedClock() steppedClock { return steppedClock{clock.NewFake(clock.Wall())} }
