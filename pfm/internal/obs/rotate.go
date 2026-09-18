package obs

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"hostops/pfm/internal/clock"
)

// DefaultKeepFiles and DefaultMaxMB are the rotation the fleet ships with when
// pfm.config.json says nothing: five files of eight megabytes, an upper bound
// of 40 MB of activity per pfm home.
const (
	DefaultKeepFiles = 5
	DefaultMaxMB     = 8
)

// rotator is the size-capped JSON-lines sink: it appends to path and, when the
// next record would carry the file past maxBytes, renames pfm.jsonl to
// pfm.jsonl.1 (shifting 1→2 … keep-1, dropping the oldest) and opens a fresh
// one; prune (retain.go) then drops the generations past keepDays. Whichever
// limit is reached first wins. In-tree on purpose — a rotation library would
// be a dependency for twenty lines (§ Destinations and environments).
type rotator struct {
	mutex    sync.Mutex
	path     string
	keep     int
	maxBytes int64
	keepDays int
	timing   clock.Clock
	file     *os.File
	size     int64
}

// newRotator opens path for append, creating its directory, and reports the
// size already on disk so the first write rotates when it must. keepDays is
// the time limit (0 disables it), measured on timing.
func newRotator(path string, keep, maxMB, keepDays int, timing clock.Clock) (*rotator, error) {
	if keep < 1 {
		keep = DefaultKeepFiles
	}
	if maxMB < 1 {
		maxMB = DefaultMaxMB
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create activity log directory %s: %w", filepath.Dir(path), err)
	}
	opened := &rotator{
		path: path, keep: keep, maxBytes: int64(maxMB) * 1024 * 1024, keepDays: keepDays, timing: timing,
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

// Write appends one record, rotating first when it would not fit.
func (writer *rotator) Write(record []byte) (int, error) {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	if writer.size > 0 && writer.size+int64(len(record)) > writer.maxBytes {
		if err := writer.rotate(); err != nil {
			return 0, err
		}
	}
	written, err := writer.file.Write(record)
	writer.size += int64(written)
	if err != nil {
		return written, fmt.Errorf("write activity log %s: %w", writer.path, err)
	}
	return written, nil
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
