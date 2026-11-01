package herdr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// The blinker's cadence. Re-reading the tab observations (the fleet scan
// behind TabProvider) is the costly part of a paint, so a tick re-reads them
// only when a hook-fired painter nudged the blinker or idleRefresh passed
// since the last read; between reads an animated tab advances from the
// observations already read, the sidebar repaints only while anything is
// blocked or done, and an idle tick does no work beyond its stat calls. A
// cancelled painter restores its tabs within restoreBudget.
const (
	idleRefresh   = 10 * time.Second
	restoreBudget = 3 * time.Second
)

// indicatorCadence is the blinker's tick state; a zero duration means its
// default.
type indicatorCadence struct {
	// owner reads the start identity of the Herdr server that owns the
	// blinker; nil reads the painter's serverStart, else the socket's mtime.
	owner         func() (int64, error)
	start         int64
	startKnown    bool
	idleRefresh   time.Duration
	restoreBudget time.Duration
	lastScan      time.Time
	lastBlocked   bool
	nudgeFailed   bool
}

// blink repaints on every half-second boundary until nothing is blocked or done
// and no tab is decorated. A cancelled context ends it cleanly;
// maxPaintFailures failed repaints in a row end it with the last error. A tick
// that finds the socket's stop marker, or its record gone (ClearIndicators:
// pfm's Herdr theme is off), clears the indicators and stops the blinker,
// stopped true. A tick that finds its owner gone (the socket removed, taken by
// a new Herdr server, or refusing connections) stops it, stopped true, with
// nothing to clear. Every failure is written to stderr as it happens.
func (painter *indicatorPainter) blink(ctx context.Context) (stopped bool, err error) {
	failures := 0
	painter.startCadence()
	for {
		wait := blinkHalfPeriod - time.Duration(painter.clock.Now().UnixNano())%blinkHalfPeriod
		if err := painter.clock.Sleep(ctx, wait); err != nil {
			return false, nil // Sleep ends early only when ctx is done
		}
		stop := !painter.recorded()
		if !stop {
			marked, err := painter.stopMarked()
			if err != nil {
				painter.logf("%v", err)
				return true, err
			}
			stop = marked
		}
		if stop {
			if err := painter.clear(ctx); err != nil {
				painter.logf("%v", err)
				return true, err
			}
			return true, nil
		}
		if painter.ownerGone() {
			return true, nil
		}
		scan, due := painter.tickDue()
		if !due {
			continue
		}
		blocked, _, err := painter.paintFrom(ctx, scan)
		if err != nil {
			if ctx.Err() != nil {
				return false, nil
			}
			painter.logf("%v", err)
			if errors.Is(err, syscall.ECONNREFUSED) {
				_, _ = fmt.Fprintf(painter.stderr,
					"herdr indicators: blinker on socket %s stops: its Herdr server refuses connections\n",
					painter.socket)
				return true, nil
			}
			if painter.ownerGone() {
				return true, nil
			}
			painter.cadence.lastScan = time.Time{} // the failed tick's read is retried at the next
			failures++
			if failures >= maxPaintFailures {
				painter.logf("herdr indicators: blinker on socket %s gave up after %d failed repaints in a row",
					painter.socket, failures)
				return false, fmt.Errorf(
					"herdr indicators: blinker on socket %s gave up after %d failed repaints in a row: %w",
					painter.socket,
					failures,
					err,
				)
			}
			continue
		}
		if scan {
			painter.cadence.lastScan = painter.clock.Now()
		}
		failures = 0
		if !blocked && !painter.hasDone && !painter.hasTabs {
			return false, nil
		}
	}
}

// startCadence starts the blinker's ticks: the paint just made read the tabs,
// and the owner's start identity is the one the socket holds now.
func (painter *indicatorPainter) startCadence() {
	painter.cadence.lastScan = painter.clock.Now()
	painter.cadence.startKnown = false
	painter.ownerGone()
}

// tickDue says whether this tick paints (due) and whether it re-reads the tab
// observations (scan). A painter without a tab provider paints every tick.
func (painter *indicatorPainter) tickDue() (scan, due bool) {
	if painter.tabs == nil {
		return false, true
	}
	refresh := painter.cadence.idleRefresh
	if refresh <= 0 {
		refresh = idleRefresh
	}
	scan = painter.takeNudge() || painter.clock.Now().Sub(painter.cadence.lastScan) >= refresh
	return scan, scan || painter.cadence.lastBlocked || painter.hasDone || painter.tabsAnimate()
}

// tabsAnimate says whether an observed tab plays more than one frame.
func (painter *indicatorPainter) tabsAnimate() bool {
	for i := range painter.tabObservations {
		d := &painter.tabObservations[i]
		if d.Mode != TabStatusOff && d.State != "" && len(tabMarkers(d.State, d.Mode, d.Glyphs)) > 1 {
			return true
		}
	}
	return false
}

// ownerGone says whether the Herdr server the blinker started under is gone:
// its socket file removed, or replaced by a new server's. The first identity
// read is the start; an identity that cannot be read otherwise never stops
// the blinker. The reason is written to stderr.
func (painter *indicatorPainter) ownerGone() bool {
	id, err := painter.ownerIdentity()
	cadence := &painter.cadence
	var reason string
	switch {
	case !cadence.startKnown:
		if err == nil {
			cadence.start, cadence.startKnown = id, true
		}
		return false
	case errors.Is(err, os.ErrNotExist):
		reason = "its socket is gone"
	case err == nil && id != cadence.start:
		reason = "a new Herdr server took its socket"
	default:
		return false
	}
	_, _ = fmt.Fprintf(painter.stderr, "herdr indicators: blinker on socket %s stops: %s\n", painter.socket, reason)
	return true
}

func (painter *indicatorPainter) ownerIdentity() (int64, error) {
	read := painter.cadence.owner
	if read == nil {
		read = painter.serverStart
	}
	if read == nil {
		info, err := os.Stat(painter.socket)
		if err != nil {
			return 0, fmt.Errorf("stat Herdr socket %s: %w", painter.socket, err)
		}
		return info.ModTime().UnixNano(), nil
	}
	return read()
}

// nudgePath is the socket's nudge: a file a hook-fired painter that found the
// blinker running leaves, so the blinker re-reads the tabs at its next tick.
func (painter *indicatorPainter) nudgePath() string {
	return filepath.Join(painter.lockDir, "indicators-"+socketKey(painter.socket)+".nudge")
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

// restoreContext bounds the restore a cancelled painter makes: it outlives the
// cancellation by restoreBudget at most.
func (painter *indicatorPainter) restoreContext(ctx context.Context) (context.Context, context.CancelFunc) {
	budget := painter.cadence.restoreBudget
	if budget <= 0 {
		budget = restoreBudget
	}
	return context.WithTimeout(context.WithoutCancel(ctx), budget)
}
