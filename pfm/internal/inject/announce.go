package inject

import (
	"context"

	pfmengine "hostops/pfm/internal/engine"
)

// PaneAnnouncer is the optional half of Tmux a pane notice needs. The real
// TmuxInjector implements it; a Tmux that does not is not an error — the
// notice is a courtesy to whoever is looking at the pane, the arming is the
// contract.
type PaneAnnouncer interface {
	Display(ctx context.Context, socketPath, target, text string) error
}

// WaitingFor states the then waiter's contract in words. It is the ONE
// spelling for both places the operator can read it — the waiter's own first
// log line (hookentry.Then) and the pane notice ScheduleAfterCurrentTurn puts
// up (announceArmed) — so the log and the pane can never disagree about what
// the waiter is waiting for.
//
// selfTarget is the shape where the pane being watched is also the pane that
// asked (SteerSpawn.SelfTarget): the waiter must first see the caller's own
// turn end. engineID is the target's engine as Target.Engine spells it; a
// Codex pane is named out loud because its contract is weaker — the Codex
// busy/compaction footer is not pinned, so with no observed turn boundary the
// waiter leaves the steer UNDELIVERED rather than typing into a compaction
// (DeliverThen).
func WaitingFor(selfTarget bool, engineID string) string {
	what := "the current turn to end"
	if selfTarget {
		what = "one idle sample, then this turn's compaction receipt or a stable idle"
	}
	if engineID == string(pfmengine.Codex) {
		return "engine codex · " + what +
			" — no steady-idle fallback until the Codex footer is pinned; an unobserved boundary leaves the steer undelivered"
	}
	return what
}

// announceArmed puts the reload-hold style notice (reload.go announcePane) on
// the pane a steer was just armed on. A Display failure is a warning on the
// log, never a refusal: the waiter is already running.
func (engine *Engine) announceArmed(ctx context.Context, target Target, request Request, selfTarget bool) {
	announcer, ok := engine.tmux.(PaneAnnouncer)
	if !ok {
		return
	}
	subject := "steer"
	if isCompactCommand(request.Message) {
		subject = "compact"
	}
	text := subject + " armed — waiting for: " + WaitingFor(selfTarget, target.Engine)
	if err := announcer.Display(ctx, target.SocketPath, target.Pane, text); err != nil {
		engine.warnf("pfm: could not display %q on %q: %v\n", text, target.Pane, err)
	}
}
