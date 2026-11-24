package tmux

import (
	"context"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

// defaultTimeout bounds a tmux call whose context carries no deadline: a
// stopped or wedged server accepts the connection and never answers, so an
// unbounded call hangs its caller and leaves its client blocked forever.
const defaultTimeout = 10 * time.Second

// boundClockKey is the context key of the clock a call's own bound is timed on
// (WithBoundClock). Nothing in production sets it.
type boundClockKey struct{}

// WithBoundClock is ctx whose tmux calls time the bound Exec and ExecWithin
// add on c: a test hands its fake clock here and moves it past the bound, so
// a call to a server that never answers ends without the test waiting the
// bound out. A context that carries its own deadline is never bounded here.
func WithBoundClock(ctx context.Context, c clock.Clock) context.Context {
	return context.WithValue(ctx, boundClockKey{}, c)
}

// boundedWithin is ctx bounded by timeout: on the clock ctx carries
// (WithBoundClock), else through context.WithTimeout itself — clock.WithTimeout's
// real path — so a bound that never fires is no armed real wait in every
// package whose tests reach a tmux call.
func boundedWithin(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if c, carried := ctx.Value(boundClockKey{}).(clock.Clock); carried && c != nil {
		return clock.WithTimeout(ctx, c, timeout)
	}
	return context.WithTimeout(ctx, timeout)
}
