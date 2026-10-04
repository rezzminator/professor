package inject

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// This file is the one answer to "has this chat's input come up yet": a
// booting TUI swallows whatever reaches it before its composer is live — the
// 2026-10-04 launch race typed five Codex briefs into the startup splash and
// lost every one. inject waits on it before typing into a Codex pane;
// internal/spawn's submit proof and internal/reload read the same row.

const (
	// ComposerHoldReads is how many consecutive captures must show a composer
	// row before the input counts as live: one read can catch a splash frame
	// that paints the glyph before the TUI takes keys.
	ComposerHoldReads = 3
	// CodexComposerWait bounds the wait for a Codex composer when the caller
	// names none: a cold launch with MCP servers booting is seconds, not
	// tens of seconds, so a pane still bare at the bound is stuck, not slow.
	CodexComposerWait = 45 * time.Second
	composerWaitPoll  = 200 * time.Millisecond
)

// ErrStartupSplash is AwaitComposerRow's timeout: the pane never showed a
// composer row it held, so anything typed would have landed on the startup
// splash (or another pre-composer screen) and been lost.
var ErrStartupSplash = errors.New("still on the startup splash: no composer row came up")

// ComposerRowShown reports whether a capture draws a composer row at all —
// the line LastComposerLine finds. A screen without one cannot take a message.
func ComposerRowShown(capture string) bool {
	return lastComposerLine(capture) != ""
}

// ComposerWait configures AwaitComposerRow. Read captures the pane; the zero
// Clock, Bound, Poll and Hold select clock.Real, CodexComposerWait, 200ms
// and ComposerHoldReads.
type ComposerWait struct {
	Read  func(context.Context) (string, error)
	Clock clock.Clock
	Bound time.Duration
	Poll  time.Duration
	Hold  int
}

// AwaitComposerRow polls until a composer row shows on Hold consecutive
// reads and returns that capture. Past Bound it returns the last capture with
// an error wrapping ErrStartupSplash; a failed read returns at once with that
// read's error, and a cancelled context with the context's — three verdicts a
// caller can tell apart, none of them an empty success.
func AwaitComposerRow(ctx context.Context, wait ComposerWait) (string, error) {
	if wait.Read == nil {
		return "", errors.New("await composer row: no pane reader given")
	}
	clk, bound, poll, hold := wait.Clock, wait.Bound, wait.Poll, wait.Hold
	if clk == nil {
		clk = clock.Real
	}
	if bound <= 0 {
		bound = CodexComposerWait
	}
	if poll <= 0 {
		poll = composerWaitPoll
	}
	if hold <= 0 {
		hold = ComposerHoldReads
	}
	deadline := clk.Now().Add(bound)
	streak := 0
	for {
		capture, err := wait.Read(ctx)
		if err != nil {
			return "", fmt.Errorf("read the pane while waiting for its composer: %w", err)
		}
		if ComposerRowShown(capture) {
			streak++
			if streak >= hold {
				return capture, nil
			}
		} else {
			streak = 0
		}
		if clk.Now().After(deadline) {
			return capture, fmt.Errorf("%w (none held %d reads within %s)", ErrStartupSplash, hold, bound)
		}
		if err := clk.Sleep(ctx, poll); err != nil {
			return capture, fmt.Errorf("wait for the composer: %w", err)
		}
	}
}

// awaitInputScreen is injectResolved's last read before any key: the
// folder-trust dialog is refused by name, and a Codex pane is held until its
// composer row comes up. A refusal lands in base (Code set); the returned
// capture is the screen the delivery goes on reading.
func (engine *Engine) awaitInputScreen(
	ctx context.Context,
	base *Result,
	target Target,
	paneEngine pfmengine.ID,
	capture string,
) (string, error) {
	if refused, held := refuseTrustDialog(*base, target.Pane, capture); held {
		*base = refused
		return capture, nil
	}
	if paneEngine != pfmengine.Codex {
		return capture, nil
	}
	ready, err := AwaitComposerRow(ctx, ComposerWait{
		Read:  func(ctx context.Context) (string, error) { return engine.capture(ctx, target, 0) },
		Clock: engine.options.Clock,
		Bound: engine.options.ComposerWait,
		Poll:  engine.options.Poll,
	})
	switch {
	case errors.Is(err, ErrStartupSplash):
		base.Code = CodeUndelivered
		base.Status = "undelivered"
		base.Message = fmt.Sprintf(
			"ABORT: Codex pane %q is %v; nothing was typed — retry once its composer shows",
			target.Pane,
			err,
		)
		base.Proof = captureLastLines(ready, engine.options.ProofLines)
		return ready, nil
	case err != nil && ctx.Err() != nil:
		return capture, fmt.Errorf("inject into %q: %w", target.Pane, err)
	case err != nil:
		base.Code = CodeDead
		base.Message = fmt.Sprintf("target pane died while waiting for its Codex composer: %v", err)
		return capture, nil
	}
	if refused, held := refuseTrustDialog(*base, target.Pane, ready); held {
		*base = refused
	}
	return ready, nil
}
