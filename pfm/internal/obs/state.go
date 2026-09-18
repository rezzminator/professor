package obs

import (
	"context"
	"log/slog"
	"time"
)

// compState is the component every coordinator transition records under.
const compState = "state"

// Transition is the state door (spec § Middleware, `state`): call it as a
// coordinator leaves prior for next, naming the cause, and call the returned
// function once with the result. It writes one state.transition record —
// the coordinator under kind, prior, next, cause, dur_ms — INFO when the
// transition completed, ERROR with err when it did not. cause is a short
// shape ("caller turn ended", "/exit sent"), never a prompt, transcript or
// pane capture; Scrub stays the last line behind it.
func Transition(ctx context.Context, comp, prior, next, cause string) func(err error) {
	started := current(ctx).timing.Now()
	return func(err error) {
		recordTransition(ctx, comp, prior, next, cause, started, err)
	}
}

func recordTransition(ctx context.Context, comp, prior, next, cause string, started time.Time, err error) {
	record(ctx, compState, "state.transition", errorLevel(err, slog.LevelInfo), started, err,
		slog.String("kind", comp), slog.String("prior", prior), slog.String("next", next), slog.String("cause", cause))
}

// Trail is Transition for a linear coordinator (reload, inject, a kill):
// NewTrail names the initial state, each Reach records the transition from
// the state before it — dur_ms is the time spent getting there — and End
// records the last state closing as done, or failed with err. A failure is
// therefore attributed to the state the coordinator was in when it struck.
type Trail struct {
	ctx     context.Context
	comp    string
	state   string
	started time.Time
	ended   bool
}

// NewTrail starts a trail for the coordinator comp in state initial.
func NewTrail(ctx context.Context, comp, initial string) *Trail {
	return &Trail{ctx: ctx, comp: comp, state: initial, started: current(ctx).timing.Now()}
}

// Reach records state → next for cause and makes next the current state.
func (trail *Trail) Reach(next, cause string) {
	if trail.ended {
		return
	}
	recordTransition(trail.ctx, trail.comp, trail.state, next, cause, trail.started, nil)
	trail.state = next
	trail.started = current(trail.ctx).timing.Now()
}

// End closes the trail: state → done, or state → failed with err. A second
// End writes nothing.
func (trail *Trail) End(err error) {
	if trail.ended {
		return
	}
	trail.ended = true
	next, cause := "done", "run ended"
	if err != nil {
		next, cause = "failed", "run aborted"
	}
	recordTransition(trail.ctx, trail.comp, trail.state, next, cause, trail.started, err)
}
