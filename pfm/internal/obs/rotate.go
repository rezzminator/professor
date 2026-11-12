package obs

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

// DefaultKeepFiles and DefaultMaxMB are the retention the fleet ships with
// when pfm.config.json says nothing: segments of eight megabytes, and the
// newest ten of them — 80 MB — kept with every record; an older segment keeps
// only its warnings and errors (retain.go).
const (
	DefaultKeepFiles = 10
	DefaultMaxMB     = 8
)

// The names inside a log's folder, beside its current segment {name}.{ext}:
// {name}.segments/ holds the older whole segments, each named by the UTC time
// it closed, and {name}.kept/ one file per UTC day of the warnings and errors
// of the segments that fell behind the kept size. Both names sort by time.
const (
	segmentsSuffix = ".segments"
	keptSuffix     = ".kept"
	segmentStamp   = "2006-01-02T15-04-05.000Z"
	keptStamp      = "2006-01-02"
)

// reattachAfter is how long a writer appends without comparing its file with
// the one at the path. A writer another process rolled past appends at most
// this long to the closed segment, which stays whole for hours; one that idled
// longer looks first, so no record lands in a segment already reduced.
const reattachAfter = time.Second

// OpenRotatingLog opens a private append-only diagnostic sink using the activity
// log's cross-process roll and error reporting, without replacing the process
// logger. keep counts the segments of maxMB kept; an older one is deleted whole.
// Callers bound each record below that cap and close the returned writer.
func OpenRotatingLog(path string, keep, maxMB int, stderr io.Writer) (io.WriteCloser, error) {
	return newRotator(path, keep, maxMB, false, clock.Real, stderr)
}

// failureReportInterval rate-limits how often a write/roll failure repeats on
// stderr: the first failure after a healthy run is always reported, and after
// that at most once per interval — a full disk must not turn every record's
// failed write into its own stderr line.
const failureReportInterval = 5 * time.Minute

// rotator is the segmented JSON-lines sink every process of a home appends to.
// One record is one write(2) on an O_APPEND descriptor, which the kernel keeps
// whole against every other appender, so a record takes no lock and no stat:
// the descriptor's offset after the append is the segment's size (others'
// appends included), and passing segmentBytes rolls it. The roll alone takes
// the home-wide flock on path+".lock": it renames the current segment into
// {name}.segments/ unless another process already did, attaches to the fresh
// file, and applies retention. In-tree on purpose — a rotation library would be
// a dependency for this much (§ Destinations and environments).
//
// A write or roll failure AFTER open would otherwise vanish: slog.Handler's
// Handle discards the error Write returns, so "no records" and "logging is
// broken" would read identically. stderr, lastReported and failing surface it
// once (rate-limited), never breaking the wrapped call, and announce recovery.
type rotator struct {
	mutex        sync.Mutex
	path         string
	segmentBytes int64
	keepBytes    int64
	reduce       bool // a segment past keepBytes keeps its warnings and errors; without, it is deleted
	timing       clock.Clock
	stderr       io.Writer
	file         *os.File
	identity     os.FileInfo // what file was when opened; the path is compared against it
	checked      time.Time   // when the path was last compared against file
	failing      bool
	lastReported time.Time
}

// newRotator opens path for append, creating its folder. keep segments of maxMB
// are kept whole; reduce keeps an older one's warnings and errors. A later
// write/roll failure is reported on stderr (nil defaults to os.Stderr).
func newRotator(path string, keep, maxMB int, reduce bool, timing clock.Clock, stderr io.Writer) (*rotator, error) {
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
	segment := int64(maxMB) * 1024 * 1024
	opened := &rotator{
		path: path, segmentBytes: segment, keepBytes: int64(keep) * segment, reduce: reduce,
		timing: timing, stderr: stderr,
	}
	if err := opened.reopen(); err != nil {
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
	writer.file, writer.identity, writer.checked = file, info, writer.timing.Now()
	return nil
}

// attachLocked makes writer.file the file at path when it is not: forced, or
// once reattachAfter has passed since the last look. The caller holds the mutex.
func (writer *rotator) attachLocked(force bool) error {
	now := writer.timing.Now()
	if writer.file != nil && !force && now.Sub(writer.checked) < reattachAfter {
		return nil
	}
	writer.checked = now
	onDisk, err := os.Stat(writer.path)
	if err == nil && writer.file != nil && os.SameFile(writer.identity, onDisk) {
		return nil
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat activity log %s: %w", writer.path, err)
	}
	if err := writer.dropHandle(); err != nil {
		return err
	}
	return writer.reopen()
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

// Write appends one record, then rolls the segment when the append carried it
// past segmentBytes. A failed append is returned (slog.Handler.Handle discards
// it, but the contract is "never silent," not "never returned") and ALSO
// surfaced on stderr, rate-limited; a failed roll costs no record and is
// reported the same way.
func (writer *rotator) Write(record []byte) (int, error) {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	if err := writer.attachLocked(false); err != nil {
		writer.reportFailureLocked(err)
		return 0, err
	}
	written, err := writer.file.Write(record)
	if err != nil {
		// The handle that failed is dropped: the next write opens the path afresh.
		wrapped := errors.Join(fmt.Errorf("write activity log %s: %w", writer.path, err), writer.dropHandle())
		writer.reportFailureLocked(wrapped)
		return written, wrapped
	}
	end, err := writer.file.Seek(0, io.SeekCurrent)
	if err != nil {
		err = fmt.Errorf("read activity log offset %s: %w", writer.path, err)
	} else if end > writer.segmentBytes {
		err = writer.rollLocked()
	}
	if err != nil {
		writer.reportFailureLocked(err)
		return written, nil
	}
	writer.reportRecoveredLocked()
	return written, nil
}

// rollLocked closes the full segment under the home-wide lock: renamed into
// {name}.segments/ when it is still the file at path, else left to the process
// that already rolled it; then the writer attaches to the fresh file and, when
// it rolled, applies retention. The caller holds the mutex.
func (writer *rotator) rollLocked() (err error) {
	lock, err := os.OpenFile(writer.path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open activity log lock %s: %w", writer.path+".lock", err)
	}
	defer func() {
		if closeErr := lock.Close(); closeErr != nil { // closing releases the flock
			err = errors.Join(err, fmt.Errorf("close activity log lock %s: %w", writer.path+".lock", closeErr))
		}
	}()
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if err != nil {
		return fmt.Errorf("lock activity log %s: %w", writer.path+".lock", err)
	}
	onDisk, err := os.Stat(writer.path)
	rolled := err == nil && os.SameFile(writer.identity, onDisk)
	if rolled {
		if err := writer.closeSegment(); err != nil {
			return err
		}
	}
	if err := writer.attachLocked(true); err != nil {
		return err
	}
	if !rolled {
		return nil
	}
	return writer.retain()
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

// processWriter is the activity log OpenLog opened, guarded by processMutex.
var processWriter *rotator

// RelocateLog moves this process's activity log to path, the root pfm install
// just moved the home's logs to; a process that opened none has nothing to move.
func RelocateLog(path string) error {
	processMutex.Lock()
	defer processMutex.Unlock()
	if processWriter == nil {
		return nil
	}
	return processWriter.relocate(path)
}

// relocate moves the writer to path: the process's own log after pfm install
// moved the home's logs to their root.
func (writer *rotator) relocate(path string) error {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	if err := writer.dropHandle(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create activity log directory %s: %w", filepath.Dir(path), err)
	}
	writer.path = path
	return writer.reopen()
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
