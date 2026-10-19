package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/clock"
)

// The theme-off stop marker and the lock that orders its write against the
// painters' writes: pfm's Herdr theme off always wins. A painter never removes
// the marker; only the theme on retires it (RetireIndicatorStops).

// errThemeOff says pfm's Herdr theme is off: the socket's stop marker is there,
// so the paint that met it writes nothing more, and the theme off's clear leaves
// the sidebar and tabs as it finds them.
var errThemeOff = errors.New("herdr indicators: pfm's Herdr theme was turned off")

// stopPath is the socket's stop marker: ClearIndicators writes it before it
// clears, so a running blinker stops at its next tick even while a failed
// clear keeps the record for the retry.
func (painter *indicatorPainter) stopPath() string {
	return filepath.Join(painter.lockDir, "indicators-"+socketKey(painter.socket)+".stop")
}

// stopMarked says whether the socket's stop marker is there. It opens the
// marker through OpenStateFile, so a FIFO, a directory or a symlink at its path
// is a named error, never a hang and never a missing marker.
func (painter *indicatorPainter) stopMarked() (bool, error) {
	file, err := OpenStateFile(painter.lockDir, filepath.Base(painter.stopPath()), os.O_RDONLY, "indicator stop marker")
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("herdr indicators: %s: %w", painter.stopPath(), err)
	}
	if err := file.Close(); err != nil {
		return true, fmt.Errorf("herdr indicators: close indicator stop marker %s: %w", painter.stopPath(), err)
	}
	return true, nil
}

// RetireIndicatorStops removes every socket's stop marker from lockDir: the
// step pfm's Herdr theme on takes before it links the plugin, since the theme
// off's marker outlives it so that a painter that starts later stands down. A
// missing lockDir holds no marker; a marker that cannot be removed is an error.
func RetireIndicatorStops(lockDir string) error {
	entries, err := os.ReadDir(lockDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("herdr indicators: read lock directory %s: %w", lockDir, err)
	}
	var failures []error
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "indicators-") || !strings.HasSuffix(name, ".stop") {
			continue
		}
		path := filepath.Join(lockDir, name)
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			failures = append(failures, fmt.Errorf("herdr indicators: retire stop marker %s: %w", path, err))
		}
	}
	return errors.Join(failures...)
}

// record writes the socket's record unless it already holds the socket,
// atomically, so a reader never sees it half-written. A stop marker there means
// pfm's Herdr theme is off, however long ago the theme off ran: record answers
// errThemeOff, leaves the marker and writes nothing. It runs under the stop
// lock, which also serializes the marker's write (writeStop) and every paint
// write.
func (painter *indicatorPainter) record(ctx context.Context) error {
	return painter.withStopLock(ctx, func() error {
		marked, err := painter.stopMarked()
		if err != nil {
			return err
		}
		if marked {
			return errThemeOff
		}
		path := painter.recordPath()
		held, err := os.ReadFile(path)
		switch {
		case err == nil && string(held) == painter.socket:
			painter.recordWritten = true
			return nil
		case err != nil && !errors.Is(err, os.ErrNotExist):
			return fmt.Errorf("herdr indicators: read socket record %s: %w", path, err)
		}
		if err := atomicfile.Write(path, []byte(painter.socket), 0o600); err != nil {
			return fmt.Errorf("herdr indicators: write socket record %s: %w", path, err)
		}
		painter.recordWritten = true
		return nil
	})
}

// stopLockPath is the lock that orders the stop marker's write against the
// writes of every paint of the socket; it is held for one write, never for a
// run, unlike the blinker lock.
func (painter *indicatorPainter) stopLockPath() string {
	return filepath.Join(painter.lockDir, "indicators-"+socketKey(painter.socket)+".stoplock")
}

func (painter *indicatorPainter) withStopLock(ctx context.Context, work func() error) error {
	if err := os.MkdirAll(painter.lockDir, 0o700); err != nil {
		return fmt.Errorf("herdr indicators: create lock directory %s: %w", painter.lockDir, err)
	}
	return withFlock(ctx, painter.stopLockPath(), "indicator stop", work)
}

// writeStop writes the stop marker under the stop lock, its body naming the
// theme off's process and time for a reader of the lock directory.
func (painter *indicatorPainter) writeStop(ctx context.Context) error {
	body := fmt.Sprintf("%d %d", os.Getpid(), clock.Real.Now().UnixNano())
	return painter.withStopLock(ctx, func() error {
		if err := atomicfile.Write(painter.stopPath(), []byte(body), 0o600); err != nil {
			return fmt.Errorf("herdr indicators: write stop marker %s: %w", painter.stopPath(), err)
		}
		return nil
	})
}

// write sends one paint request. A paint of a theme-on run (record ran) holds
// the stop lock across the check and the send: it stands down, errThemeOff,
// once the stop marker is there or the socket's record is gone, and a theme off
// that writes its marker meanwhile waits for the send to finish, so its clear
// then finds what the send wrote. The painter's own clear is that theme off's
// work and sends unguarded.
func (painter *indicatorPainter) write(ctx context.Context, line []byte) error {
	_, err := painter.guardedCall(ctx, painter.socket, line)
	return err
}

// guardedCall is painter.call under write's guard, its answers returned: the
// tab status painter (reconcileTab) reads and renames through it, so a theme
// off stops a tab rename as it stops a token write.
func (painter *indicatorPainter) guardedCall(
	ctx context.Context,
	socket string,
	lines ...[]byte,
) (results []json.RawMessage, err error) {
	if !painter.recordWritten || painter.clearing || painter.lockDir == "" {
		return painter.call(ctx, socket, lines...)
	}
	err = painter.withStopLock(ctx, func() error {
		marked, markErr := painter.stopMarked()
		if markErr != nil {
			return markErr
		}
		if marked || !painter.recorded() {
			return errThemeOff
		}
		results, err = painter.call(ctx, socket, lines...)
		return err
	})
	return results, err
}
