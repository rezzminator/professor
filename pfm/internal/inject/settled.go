package inject

import (
	"context"
	"fmt"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// SettledTurn is this package's ONE wait for "the turn the pane is running
// now is over". The --then waiter rides out a self-compaction with it
// (Engine.waitForSettledTurn), and internal/reload holds its own tmux seam and
// its own captures — so the wait takes the capture and the sleep as seams
// rather than an Engine, and neither surface grows a second copy of the
// turn-identification rules below.
type SettledTurn struct {
	// Capture takes ONE reading of the pane, history included
	// (paneScrollbackLines). Its error is a failure to look, never an
	// observation — see samplePane.
	Capture func(ctx context.Context) (string, error)
	// Sleep is the clock seam every wait in the loop crosses.
	Sleep func(ctx context.Context, duration time.Duration)
	// Pane names the target in the baseline error; nothing else reads it.
	Pane string
	// Engine is the target's engine, so the busy half of every sample reads
	// the footer THIS TUI renders (IsBusyFor). Empty keeps the historical
	// Claude/Codex rule, which is what a caller that could not resolve an
	// engine has always used.
	Engine pfmengine.ID
	// SelfTarget marks the shape where the pane being watched is the pane
	// that asked, so the CALLER's own turn must be seen to end first.
	SelfTarget bool
	Min        time.Duration
	Poll       time.Duration
	Settle     time.Duration
	BusyTries  int
	IdleTries  int
	IdleStable int
}

// paneSample is one observation of the target pane. Busy alone cannot answer
// "is the turn I was sent to ride out over yet" — it is true for ANY turn,
// including the caller's own and the one the session starts by itself after a
// compaction. The receipt is the only positive evidence in the pane that a
// compaction actually ran.
//
// receipts is a COUNT, not a bool, and it is counted over the captured
// history rather than the visible fold. A bool answers "a receipt is on
// screen", which is true both for the compaction this waiter is riding out
// and for an OLD receipt that merely scrolled back into view (a footer
// collapse, a pane resize, a redraw) — two states a waiter must never blur,
// because only one of them is this turn's boundary. A count over the history
// can only go UP when a receipt is genuinely printed: a stale one that
// re-enters the visible fold was already in the baseline's count.
type paneSample struct {
	busy     bool
	receipts int
}

// paneScrollbackLines bounds the history samplePane reads. It has to reach
// past the visible fold — that is the whole point of counting receipts over
// history — but a chat pane's full retained buffer is polled once a second
// for up to ten minutes, so the window is bounded rather than FullScrollback.
// A receipt that scrolls out of a 2000-line window during the wait lowers the
// count and can only make the receipt door refuse to open, which falls back
// to the steady-idle path with its WARNING — the safe direction.
const paneScrollbackLines = 2000

// paneBusyTailLines is how much of that history the busy test sees. busy is a
// LIVE property of the footer at the bottom of the pane; an "esc to interrupt"
// retained in scrollback from a turn that ended long ago would read as busy
// forever, so the busy test keeps its visible-footer scope while the receipt
// count gets the history.
const paneBusyTailLines = 20

// IsFooterBusy is the busy test for a whole pane capture: IsBusyFor scoped to
// the pane's last paneBusyTailLines non-empty lines, where the live spinner and
// footer render. A whole-screen test reads the chat's own transcript prose — an
// answer saying "read 27,615 tokens" still on screen — as a turn that never
// ends.
func IsFooterBusy(engine pfmengine.ID, capture string) bool {
	return IsBusyFor(engine, lastNonEmptyLines(capture, paneBusyTailLines))
}

// samplePane returns one observation and whether the pane could be READ at
// all. A capture failure is never rendered as an observation: paneSample's
// zero value says "not busy, no receipt seen", and for receipts that polarity
// is permissive — "no compaction receipt was on screen" is an affirmative
// claim, and asserting it from a read that never happened is what let a
// single transient capture failure turn the receipt guard into its opposite.
// The caller decides what a failed read means; only Run's non-baseline
// samples may treat it as "nothing new here this poll".
func (wait SettledTurn) samplePane(ctx context.Context) (paneSample, error) {
	capture, err := wait.Capture(ctx)
	if err != nil {
		return paneSample{}, err
	}
	return paneSample{
		busy:     IsFooterBusy(wait.Engine, capture),
		receipts: countCompactionReceipts(capture),
	}, nil
}

// newReceipt reports whether this sample carries a compaction receipt the
// baseline did not — the second signal the busy-side door needs, since a
// receipt that is merely newly VISIBLE is not a receipt that is newly
// PRINTED.
func (sample paneSample) newReceipt(baseline paneSample) bool {
	return sample.receipts > baseline.receipts
}

// Run rides out the turn the PRIMARY started and reports whether
// it ever actually saw that turn.
//
// The old shape (chat.sh:1062-1077) waited for the pane to go busy and then for
// idle to hold steady. That works only if the busy it latches onto belongs to
// the primary — and busy carries no identity. For a self-inject the pane is
// already busy with the caller's own turn when the waiter wakes up, so the
// waiter would ride out the WRONG turn and then race whichever idle came first,
// losing in one of two directions depending on nothing but timing:
//
//   - caller stops promptly -> the waiter sees the idle BEFORE the queued
//     /compact has run and delivers the steer into a session that is about to
//     be compacted away, taking the steer with it.
//   - caller keeps working -> the brief idle right after the compaction is
//     shorter than the stability window, so the waiter sleeps through the one
//     usable moment and delivers on top of work that already resumed.
//
// Both are the same defect. The fix is to stop inferring the turn from a
// coincidence and identify it instead:
//
//  1. the caller's own turn must END first (an idle observation) — until then
//     nothing on screen can belong to the primary;
//  2. a turn must START after that (a busy observation) — that one is the
//     primary's;
//  3. a compaction receipt seen after BOTH is positive proof the primary was a
//     compaction and that it finished, so the first quiet sample after it is
//     the delivery point.
//
// Requiring the receipt to arrive after step 2 is what keeps step 3 from
// becoming a coincidence detector in its own right: a receipt already on screen
// when the waiter wakes up is scrollback from an EARLIER compaction and proves
// nothing about this one.
//
// Step 3 has a second door, for the pane a background sub-agent keeps busy.
// Its footer matches busyPattern for as long as the agent runs, so the pane
// NEVER reads idle: step 1 cannot complete for a self-inject, and the
// "receipt AND quiet" test never passes for anyone — the waiter burned its
// whole budget (~10 min) and then delivered on the WARNING path (Wave 8 item
// 5, beat E1.20). Sidechains are allowed — /compact works beside a background
// agent — so the receipt is the boundary: a BASELINE capture before the loop
// records whether a receipt was already on screen, and a receipt that appears
// afterwards while the pane still reads busy is this turn's, footer or no
// footer. A receipt already in the baseline stays scrollback and proves
// nothing, exactly as before. A receipt that appears at an IDLE sample before
// any turn was seen to start is still not taken as proof: that is also what a
// stale receipt uncovered by the caller's footer clearing looks like
// (TestCompactionReceiptNeedsToAppear), and the steady-idle path below still
// delivers that shape — with the WARNING that says the guarantee is weaker.
//
// Step 3 cannot apply to a primary that prints no receipt — a reload steer, a
// plain queued message, and (a NAMED gap) a Codex compaction, whose receipt
// spelling nobody here has confirmed. Those fall back to steps 1-2 plus the
// steady-idle window, which is strictly better than the old behaviour because
// the caller's own turn can no longer be mistaken for the primary's.
//
// The returned bool is false when the bound expired without ever observing a
// turn boundary. It is not an error — refusing to deliver would strand the
// chain, which is worse — but it is a WEAKER guarantee than the caller asked
// for, and DeliverThen says so on the visible result rather than only in a log.
//
// The returned ERROR is the fourth state, and the only one that is not a
// verdict about the turn: the pane could not be read even ONCE, so there is no
// baseline and no reference frame. The baseline is retried on the same poll
// cadence as every other sample until one real read lands, the receipt doors
// stay shut until it does, and a waiter that never gets one returns that error
// instead of a "false" that reads like an observation (DeliverThen turns it
// into a named undelivered result).
func (wait SettledTurn) Run(ctx context.Context) (bool, error) {
	wait.Sleep(ctx, wait.Min)

	// Step 1 exists only for a self-inject, where the pane is busy with the
	// CALLER's turn when the waiter wakes up. For any other target nothing else
	// owns that pane, so its first busy already belongs to the primary and
	// insisting on a prior idle would wait out a boundary that never comes.
	callerYielded := !wait.SelfTarget
	turnStarted := false
	stable := 0
	sinceYield := 0
	// The baseline is the receipt's reference frame: only a receipt that was
	// NOT here when the waiter woke can be this turn's. A capture that FAILED
	// is not a reference frame — it is no frame at all — so the baseline is
	// taken again on every poll until one read really lands.
	baseline, baselineErr := wait.samplePane(ctx)
	haveBaseline := baselineErr == nil

	tries := wait.BusyTries + wait.IdleTries
	for attempt := 0; attempt < tries; attempt++ {
		sample, sampleErr := wait.samplePane(ctx)
		if !haveBaseline {
			if sampleErr != nil {
				baselineErr = sampleErr
				wait.Sleep(ctx, wait.Poll)
				continue
			}
			baseline, haveBaseline, baselineErr = sample, true, nil
			wait.Sleep(ctx, wait.Poll)
			continue
		}
		// A failed sample AFTER the baseline keeps its old meaning: the zero
		// sample, one poll that saw nothing — an unreadable pane is not busy,
		// and the delivery attempt reports a dead pane truthfully rather than
		// spinning here. It carries no receipt either, so it can only delay a
		// door, never open one.

		switch {
		case !callerYielded:
			callerYielded = !sample.busy
		case !turnStarted:
			turnStarted = sample.busy
			sinceYield++
		}

		// Positive proof outranks the busy/idle dance: a receipt this turn
		// printed, seen either after the turn we watched start (whether or not a background agent's footer still reads busy)
		// or while the pane is busy at all — the sidechain shape, where the
		// compaction ran beside an agent that never lets the pane read idle.
		// A NEW receipt at an idle sample before any turn was seen to start is
		// still not proof and still falls to the steady-idle path below.
		if sample.newReceipt(baseline) && (turnStarted || sample.busy) {
			wait.Sleep(ctx, wait.Settle)
			return true, nil
		}

		if turnStarted {
			if sample.busy {
				stable = 0
			} else {
				stable++
			}
			if stable >= wait.IdleStable {
				wait.Sleep(ctx, wait.Settle)
				return true, nil
			}
		}

		// The primary's turn never began. Either it started and finished
		// inside ThenMin, or this pane does not report busy at all. Holding
		// out for a boundary that already went by would burn the whole idle
		// budget — minutes — and strand the steer, which is a worse failure
		// than delivering on a weaker guarantee. So fall back to steady idle
		// and return false, which is what puts the warning on the result
		// instead of letting a guess pass for proof.
		if callerYielded && !turnStarted &&
			sinceYield > wait.BusyTries {
			if sample.busy {
				stable = 0
			} else {
				stable++
			}
			if stable >= wait.IdleStable {
				wait.Sleep(ctx, wait.Settle)
				return false, nil
			}
		}
		wait.Sleep(ctx, wait.Poll)
	}
	wait.Sleep(ctx, wait.Settle)
	if !haveBaseline {
		return false, fmt.Errorf("capture pane %q for a settled-turn baseline: %w", wait.Pane, baselineErr)
	}
	return false, nil
}

// waitForSettledTurn is the --then waiter's seat in SettledTurn: the Engine's
// tmux and clock, its Then* bounds, and the target it was spawned for.
func (engine *Engine) waitForSettledTurn(
	ctx context.Context,
	socketPath, target string,
	selfTarget bool,
	engineID pfmengine.ID,
) (bool, error) {
	return SettledTurn{
		Engine: engineID,
		Capture: func(ctx context.Context) (string, error) {
			return engine.tmux.Capture(ctx, socketPath, target, false, paneScrollbackLines)
		},
		Sleep:      engine.sleepContext,
		Pane:       target,
		SelfTarget: selfTarget,
		Min:        engine.options.ThenMin,
		Poll:       engine.options.ThenIdlePoll,
		Settle:     engine.options.ThenSettle,
		BusyTries:  engine.options.ThenBusyTries,
		IdleTries:  engine.options.ThenIdleTries,
		IdleStable: engine.options.ThenIdleStable,
	}.Run(ctx)
}
