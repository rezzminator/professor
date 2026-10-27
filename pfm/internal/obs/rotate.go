package obs

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
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

// OpenRotatingLog opens a private append-only diagnostic sink using the activity
// log's cross-process rotation and error reporting, without replacing the process
// logger. keep includes the current file; maxMB caps each generation. Callers
// bound each record below that cap and close the returned writer.
func OpenRotatingLog(path string, keep, maxMB int, stderr io.Writer) (io.WriteCloser, error) {
	return newRotator(path, keep, maxMB, 0, clock.Real, stderr)
}

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
// Every process of a home appends to the same path, so the decision is made
// from the file on disk, never from a byte count kept in the process: under
// an exclusive flock on the sidecar path+".lock" each Write stats the path;
// when its inode is not the open descriptor's another process rotated (or
// removed) the file, and the writer attaches to the path instead of rotating
// it; otherwise the on-disk size decides. The lock is held through the append,
// so no record lands in a file another process is renaming.
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
	identity     os.FileInfo // what file was when opened; the path is compared against it
	lock         *os.File    // the sidecar path+".lock", flocked around every write
	failing      bool
	lastReported time.Time
}

// newRotator opens path for append, creating its directory and the lock
// sidecar; the first write measures the file on disk. keepDays is
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
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open activity log lock %s: %w", path+".lock", err)
	}
	opened.lock = lock
	if err := opened.reopen(); err != nil {
		if closeErr := lock.Close(); closeErr != nil {
			return nil, fmt.Errorf("%w (and close lock: %w)", err, closeErr)
		}
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

// reopen attaches to the file at path and remembers which file that is.
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
	writer.file, writer.identity = file, info
	return nil
}

// alignLocked makes writer.file the file at path and rotates it when record
// bytes more would pass maxBytes, deciding from the path on disk. The caller
// holds writer.mutex and the flock.
func (writer *rotator) alignLocked(incoming int64) error {
	onDisk, err := os.Stat(writer.path)
	switch {
	case err == nil && writer.file != nil && os.SameFile(writer.identity, onDisk):
		// Still the file this writer opened: its real size decides below.
	case err == nil || errors.Is(err, fs.ErrNotExist):
		// Another process rotated or removed the file (or a failed rotation
		// left no handle): attach to what is at the path now.
		if err := writer.dropHandle(); err != nil {
			return err
		}
		if err := writer.reopen(); err != nil {
			return err
		}
		onDisk = writer.identity
	default:
		return fmt.Errorf("stat activity log %s: %w", writer.path, err)
	}
	if onDisk.Size() > 0 && onDisk.Size()+incoming > writer.maxBytes {
		return writer.rotate()
	}
	return nil
}

// dropHandle closes a handle that no longer leads to the file at path. A
// handle already closed (os.ErrClosed) has nothing left to release.
func (writer *rotator) dropHandle() error {
	if writer.file == nil {
		return nil
	}
	err := writer.file.Close()
	writer.file = nil
	if err != nil && !errors.Is(err, os.ErrClosed) {
		return fmt.Errorf("close stale activity log handle %s: %w", writer.path, err)
	}
	return nil
}

// flock takes or releases the sidecar lock, retrying an interrupted call.
func (writer *rotator) flock(how int) error {
	for {
		err := syscall.Flock(int(writer.lock.Fd()), how)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

// Write appends one record, rotating first when it would not fit on the file
// at path (alignLocked), under the home-wide lock. A failure
// is still returned to the caller (slog.Handler.Handle discards it, but the
// contract here is "never silent," not "never returned") and is ALSO
// surfaced on stderr, rate-limited by reportFailureLocked.
func (writer *rotator) Write(record []byte) (int, error) {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	if err := writer.flock(syscall.LOCK_EX); err != nil {
		wrapped := fmt.Errorf("lock activity log %s: %w", writer.path+".lock", err)
		writer.reportFailureLocked(wrapped)
		return 0, wrapped
	}
	defer func() {
		if err := writer.flock(syscall.LOCK_UN); err != nil {
			writer.reportFailureLocked(fmt.Errorf("unlock activity log %s: %w", writer.path+".lock", err))
		}
	}()
	if err := writer.alignLocked(int64(len(record))); err != nil {
		writer.reportFailureLocked(err)
		return 0, err
	}
	written, err := writer.file.Write(record)
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

// Close releases the file handle and the lock sidecar.
func (writer *rotator) Close() error {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	var failures []error
	if writer.file != nil {
		if err := writer.file.Close(); err != nil {
			failures = append(failures, fmt.Errorf("close activity log %s: %w", writer.path, err))
		}
		writer.file = nil
	}
	if writer.lock != nil {
		if err := writer.lock.Close(); err != nil {
			failures = append(failures, fmt.Errorf("close activity log lock %s: %w", writer.path+".lock", err))
		}
		writer.lock = nil
	}
	return errors.Join(failures...)
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
