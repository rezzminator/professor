package inject

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// SettledTurn is this package's ONE wait for "the turn the pane is running
// now is over". The --then waiter uses it for the typed primary on a chained hop
// (Engine.waitForSettledTurn); internal/reload holds its own tmux seam and
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
	Engine     pfmengine.ID
	Min        time.Duration
	Poll       time.Duration
	Settle     time.Duration
	BusyTries  int
	IdleTries  int
	IdleStable int
}

// paneSample is one observation of the target pane. Busy alone cannot answer
// "is the turn I was sent to ride out over yet" — it is true for ANY turn,
// including an earlier turn and the one the session starts by itself after a
// compaction. A new receipt also proves that a chained compaction ran.
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
// ends. Inside that window a Claude transcript RECORD line is dropped too: an
// agent's `● Agent "X" finished · 46s` or a `⏺ Read 27,615 tokens` matches
// busyPattern's `· \d+s` / `\d+ tokens` arms and, sitting under an idle
// composer, kept the pane "busy" until new output scrolled it away — a
// chained waiter could miss the turn boundary (2026-09-25). For Claude,
// IsBusyFor itself reads only the engine's spinner row and interrupt hint, so
// the continuation lines of a final answer cannot hold the pane busy either.
func IsFooterBusy(engine pfmengine.ID, capture string) bool {
	tail := lastNonEmptyLines(capture, paneBusyTailLines)
	if engine != pfmengine.OpenCode {
		tail = withoutRecordLines(tail)
	}
	return IsBusyFor(engine, tail)
}

// withoutRecordLines drops every line whose first non-space rune is a Claude
// transcript record bullet (● or ⏺). The live spinner never opens with one —
// its glyphs are ✻ ✢ ✶ ✳ ✽ · — so the busy arms still see it.
func withoutRecordLines(tail string) string {
	lines := strings.Split(tail, "\n")
	kept := lines[:0]
	for _, line := range lines {
		trimmed := strings.TrimLeftFunc(stripTerminalControl(line), unicode.IsSpace)
		if strings.HasPrefix(trimmed, "●") || strings.HasPrefix(trimmed, "⏺") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
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

// Run waits for the chained primary's turn to start and end, or for a new
// compaction receipt relative to the baseline. If the turn never appears
// busy, stable idle remains a weak fallback.
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
		if !turnStarted {
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
		if !turnStarted && sinceYield > wait.BusyTries {
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
	engineID pfmengine.ID,
) (bool, error) {
	return SettledTurn{
		Engine: engineID,
		Capture: func(ctx context.Context) (string, error) {
			return engine.tmux.Capture(ctx, socketPath, target, false, paneScrollbackLines)
		},
		Sleep:      engine.sleepContext,
		Pane:       target,
		Min:        engine.options.ThenMin,
		Poll:       engine.options.ThenIdlePoll,
		Settle:     engine.options.ThenSettle,
		BusyTries:  engine.options.ThenBusyTries,
		IdleTries:  engine.options.ThenIdleTries,
		IdleStable: engine.options.ThenIdleStable,
	}.Run(ctx)
}
