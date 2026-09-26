package obs

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

// DefaultKeepFiles and DefaultMaxMB are the rotation the fleet ships with when
// pfm.config.json says nothing: five files of eight megabytes, an upper bound
// of 40 MB of activity per pfm home.
const (
	DefaultKeepFiles = 5
	DefaultMaxMB     = 8
)

// failureReportInterval rate-limits how often a write/rotate failure repeats
// on stderr once OpenLog has already opened the file successfully: the first
// failure after a healthy run is always reported, and after that at most
// once per interval — a full disk must not turn every record's failed write
// into its own stderr line.
const failureReportInterval = 5 * time.Minute

// rotator is the size-capped JSON-lines sink: it appends to path and, when the
// next record would carry the file past maxBytes, renames pfm.jsonl to
// pfm.jsonl.1 (shifting 1→2 … keep-1, dropping the oldest) and opens a fresh
// one; prune (retain.go) then drops the generations past keepDays. Whichever
// limit is reached first wins. In-tree on purpose — a rotation library would
// be a dependency for twenty lines (§ Destinations and environments).
//
// A write or rotate failure AFTER open would otherwise vanish: slog.Handler's
// Handle discards the error Write returns, so "no records" and "logging is
// broken" would read identically. stderr, lastReported and failing exist so
// that failure is surfaced once (rate-limited) instead — never breaking the
// wrapped call — and a recovered state is announced once too.
type rotator struct {
	mutex        sync.Mutex
	path         string
	keep         int
	maxBytes     int64
	keepDays     int
	timing       clock.Clock
	stderr       io.Writer
	file         *os.File
	size         int64
	failing      bool
	lastReported time.Time
}

// newRotator opens path for append, creating its directory, and reports the
// size already on disk so the first write rotates when it must. keepDays is
// the time limit (0 disables it), measured on timing. A later write/rotate
// failure is reported on stderr (nil defaults to os.Stderr).
func newRotator(path string, keep, maxMB, keepDays int, timing clock.Clock, stderr io.Writer) (*rotator, error) {
	if keep < 1 {
		keep = DefaultKeepFiles
	}
	if maxMB < 1 {
		maxMB = DefaultMaxMB
	}
	if stderr == nil {
		stderr = os.Stderr
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create activity log directory %s: %w", filepath.Dir(path), err)
	}
	opened := &rotator{
		path:     path,
		keep:     keep,
		maxBytes: int64(maxMB) * 1024 * 1024,
		keepDays: keepDays,
		timing:   timing,
		stderr:   stderr,
	}
	if err := opened.reopen(); err != nil {
		return nil, err
	}
	if err := opened.prune(); err != nil {
		if closeErr := opened.Close(); closeErr != nil {
			return nil, fmt.Errorf("%w (and close: %w)", err, closeErr)
		}
		return nil, err
	}
	return opened, nil
}

// reopen attaches to the current file and reads its size.
func (writer *rotator) reopen() error {
	file, err := os.OpenFile(writer.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open activity log %s: %w", writer.path, err)
	}
	info, err := file.Stat()
	if err != nil {
		if closeErr := file.Close(); closeErr != nil {
			return fmt.Errorf("stat activity log %s: %w (and close: %v)", writer.path, err, closeErr)
		}
		return fmt.Errorf("stat activity log %s: %w", writer.path, err)
	}
	writer.file, writer.size = file, info.Size()
	return nil
}

// Write appends one record, rotating first when it would not fit. A failure
// is still returned to the caller (slog.Handler.Handle discards it, but the
// contract here is "never silent," not "never returned") and is ALSO
// surfaced on stderr, rate-limited by reportFailureLocked.
func (writer *rotator) Write(record []byte) (int, error) {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	if writer.size > 0 && writer.size+int64(len(record)) > writer.maxBytes {
		if err := writer.rotate(); err != nil {
			writer.reportFailureLocked(err)
			return 0, err
		}
	}
	written, err := writer.file.Write(record)
	writer.size += int64(written)
	if err != nil {
		wrapped := fmt.Errorf("write activity log %s: %w", writer.path, err)
		writer.reportFailureLocked(wrapped)
		return written, wrapped
	}
	writer.reportRecoveredLocked()
	return written, nil
}

// reportFailureLocked surfaces err on stderr: always on the first failure
// since the last healthy write, and after that at most once per
// failureReportInterval. The caller holds writer.mutex.
func (writer *rotator) reportFailureLocked(err error) {
	now := writer.timing.Now()
	if writer.failing && now.Sub(writer.lastReported) < failureReportInterval {
		return
	}
	writer.failing = true
	writer.lastReported = now
	fmt.Fprintf(writer.stderr, "pfm: activity log: %v\n", err)
}

// reportRecoveredLocked announces a return to healthy writes exactly once,
// only when a prior failure was reported. The caller holds writer.mutex.
func (writer *rotator) reportRecoveredLocked() {
	if !writer.failing {
		return
	}
	writer.failing = false
	fmt.Fprintf(writer.stderr, "pfm: activity log: writes to %s recovered\n", writer.path)
}

// Close releases the file handle.
func (writer *rotator) Close() error {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	if writer.file == nil {
		return nil
	}
	err := writer.file.Close()
	writer.file = nil
	if err != nil {
		return fmt.Errorf("close activity log %s: %w", writer.path, err)
	}
	return nil
}

// rotate shifts the generations down and opens a fresh current file. The
// caller holds the mutex.
func (writer *rotator) rotate() error {
	if err := writer.file.Close(); err != nil {
		return fmt.Errorf("close activity log %s before rotation: %w", writer.path, err)
	}
	writer.file = nil
	// keep counts every file the home retains, so the oldest generation is
	// keep-1: it is dropped, and each younger one ages by one.
	oldest := writer.path + "." + strconv.Itoa(writer.keep-1)
	if err := os.Remove(oldest); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("drop oldest activity log %s: %w", oldest, err)
	}
	for generation := writer.keep - 2; generation >= 1; generation-- {
		aged := writer.path + "." + strconv.Itoa(generation+1)
		current := writer.path + "." + strconv.Itoa(generation)
		if err := os.Rename(current, aged); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("rotate activity log %s: %w", current, err)
		}
	}
	if writer.keep > 1 {
		if err := os.Rename(writer.path, writer.path+".1"); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("rotate activity log %s: %w", writer.path, err)
		}
	} else if err := os.Remove(writer.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("drop activity log %s: %w", writer.path, err)
	}
	if err := writer.reopen(); err != nil {
		return err
	}
	return writer.prune()
}
