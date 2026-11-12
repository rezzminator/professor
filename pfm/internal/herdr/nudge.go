package herdr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// NudgePath is socket's nudge in lockDir: a file that makes the socket's
// blinker re-read the tabs at its next tick. A hook-fired painter that found
// the blinker running leaves it, and so does NudgeIndicators.
func NudgePath(lockDir, socket string) string {
	return filepath.Join(lockDir, "indicators-"+socketKey(socket)+".nudge")
}

// NudgeIndicators leaves socket's nudge. A writer that changes what a tab
// shows outside a hook-fired painter (a rename) calls it once its change is
// stored: a tick that read the tabs before the change paints the old name
// over it, and without the nudge the tab keeps it until idleRefresh. A lock
// directory that does not exist has had no painter: nothing to nudge.
func NudgeIndicators(socket string) error {
	home, err := (paths.OSEnv{}).Home()
	if err != nil {
		return fmt.Errorf("nudge the blinker of socket %s: home: %w", socket, err)
	}
	err = os.WriteFile(NudgePath(paths.HerdrLockDir(home), socket), nil, 0o600)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("nudge the blinker of socket %s: %w", socket, err)
	}
	return nil
}

func (painter *indicatorPainter) nudgePath() string {
	return NudgePath(painter.lockDir, painter.socket)
}

// nudge leaves the socket's nudge; a failure is named, and the blinker's
// idleRefresh still catches the change.
func (painter *indicatorPainter) nudge() {
	if err := os.WriteFile(painter.nudgePath(), nil, 0o600); err != nil {
		painter.logf("herdr indicators: nudge the blinker of socket %s: %v", painter.socket, err)
	}
}

// takeNudge removes the socket's nudge and says whether one was there; a
// failure to remove it is named once while it lasts and reads as no nudge,
// so a stuck nudge never turns every tick into a scan.
func (painter *indicatorPainter) takeNudge() bool {
	path := painter.nudgePath()
	err := os.Remove(path)
	switch {
	case err == nil:
		painter.cadence.nudgeFailed = false
		return true
	case errors.Is(err, os.ErrNotExist):
		painter.cadence.nudgeFailed = false
		return false
	}
	if !painter.cadence.nudgeFailed {
		painter.cadence.nudgeFailed = true
		painter.logf("herdr indicators: take the nudge %s: %v", path, err)
	}
	return false
}

// runTabs is run with a tab provider: it paints and blinks while it holds the
// socket's blinker lock. A painter that finds the lock held leaves the nudge,
// then tries the lock once more, and the holder takes any nudge after it lets
// go: a tab change announced while a blinker stops (a chat born as its last
// tick read no tab) is never lost between the two, whichever lets go first. A
// nudge it cannot take reads as none (takeNudge), so it never spins.
func (painter *indicatorPainter) runTabs(ctx context.Context) error {
	for {
		release, held, err := painter.takeBlinkerLock()
		if err == nil && !held {
			painter.nudge() // the blinker re-reads the tabs at its next tick
			// The holder may have let go before the nudge landed, and found none.
			release, held, err = painter.takeBlinkerLock()
		}
		if err != nil {
			painter.logf("%v", err)
			return err
		}
		if !held {
			return nil
		}
		painter.takeNudge() // the paint below reads the tabs; a later nudge survives it
		again, err := painter.blinkTabs(ctx)
		release()
		if err != nil || !again || !painter.takeNudge() {
			return err
		}
	}
}

// blinkTabs is one hold of the blinker lock: a paint, then the blink while any
// tab is decorated or anything is blocked or done. again says it stopped only
// because nothing was left to paint, so a nudge left meanwhile takes the lock
// again; a cancelled painter restores its tabs before it lets go.
func (painter *indicatorPainter) blinkTabs(ctx context.Context) (again bool, err error) {
	blocked, _, err := painter.paint(ctx)
	if err != nil {
		return false, painter.failed(err)
	}
	if !blocked && !painter.hasDone && !painter.hasTabs {
		return true, nil
	}
	stopped, err := painter.blink(ctx)
	if ctx.Err() != nil {
		restoreCtx, cancel := painter.restoreContext(ctx)
		defer cancel()
		if restoreErr := painter.restoreTabs(restoreCtx); restoreErr != nil {
			painter.logf("%v", restoreErr)
			return false, errors.Join(err, restoreErr)
		}
		return false, err
	}
	return !stopped && err == nil, err
}
