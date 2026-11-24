package mockengine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

// busy paints the working footer until the duration passes on the mock's clock (or, with a gate
// file, until it disappears), pumping keys meanwhile. Its end is armed on the clock before the
// first footer paints, so a test that sees the footer can step the clock to it. It returns
// (false, nil) when Escape interrupted the turn or the context ended, and a non-nil error when the
// gate file could not be statted for a reason other than its confirmed absence — a permission
// error or a vanished parent directory must never be read as "the gate is gone, proceed" the same
// way removal is.
func (loop *paneLoop) busy(started time.Time, duration time.Duration, gate string) (bool, error) {
	screen := loop.screen
	clk := loop.proc.clock()
	deadline := clk.Now().Add(duration)
	var due <-chan time.Time
	if gate == "" && duration > 0 {
		timer := clk.NewTimer(duration)
		defer timer.Stop()
		due = timer.C()
	}
	for {
		screen.footer = loop.engine.busyLine(clk.Now().Sub(started), loop.proc.script.Tokens)
		loop.paintTyped()
		done := false
		if gate != "" {
			_, err := os.Stat(gate)
			switch {
			case err == nil:
				done = false
			case errors.Is(err, fs.ErrNotExist):
				done = true
			default:
				return false, fmt.Errorf("stat hold gate %s: %w", gate, err)
			}
		} else {
			done = !clk.Now().Before(deadline)
		}
		if done {
			return true, nil
		}
		select {
		case <-loop.proc.ctx.Done():
			return false, nil
		case event, ok := <-loop.keys:
			if !ok {
				return false, nil
			}
			switch event.kind {
			case keyEscape:
				return false, nil
			case keyEnter:
				if line := screen.draft.take(); line != "" {
					screen.queued = append(screen.queued, line)
				}
			default:
				screen.draft.apply(event)
			}
		case <-loop.statusDue:
			loop.refreshStatus(loop.usage)
		case <-due:
		case <-loop.proc.lookAgain():
		}
	}
}
