package herdr

import (
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
