// Package atomicfile is the one atomic file writer: every file pfm replaces
// whole — config, caches, crumbs, receipts, staged assets — goes through Write,
// so a reader sees the old content or the new, never a torn write, and a
// failure leaves the old file and no scratch behind.
package atomicfile

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// removeScratch deletes an unpublished scratch file; a variable so a test can
// make the removal fail and watch the failure surface.
var removeScratch = os.Remove

// Write replaces path with content in one rename: a scratch file beside path
// (".<name>.tmp-*", so the rename never crosses a filesystem) is written, given
// mode's permission bits, synced and closed, then renamed over path. A missing
// parent directory is created 0o700; a caller that wants a wider directory
// creates it first.
func Write(path string, content []byte, mode fs.FileMode) (err error) {
	return publish(path, mode, func(scratch *os.File) error {
		_, err := scratch.Write(content)
		return err
	})
}

// ErrTooLarge reports a WriteFrom stream that ran past its limit; the target
// is left untouched and no scratch remains.
var ErrTooLarge = errors.New("content exceeds the size limit")

// WriteFrom streams r into path with Write's guarantees, never holding the
// whole body in memory. limit > 0 caps the stream: one byte past it is
// ErrTooLarge and nothing is published. It returns the bytes published.
func WriteFrom(path string, r io.Reader, mode fs.FileMode, limit int64) (written int64, err error) {
	err = publish(path, mode, func(scratch *os.File) error {
		source := r
		if limit > 0 {
			source = io.LimitReader(r, limit+1)
		}
		n, copyErr := io.Copy(scratch, source)
		written = n
		if copyErr != nil {
			return copyErr
		}
		if limit > 0 && n > limit {
			return fmt.Errorf("%w (%d bytes)", ErrTooLarge, limit)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return written, nil
}

// publish is the one scratch-then-rename sequence Write and WriteFrom share:
// fill writes the scratch file, which is then synced, closed and renamed over
// path; any failure removes the scratch and leaves path as it was.
func publish(path string, mode fs.FileMode, fill func(*os.File) error) (err error) {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("write %s: create directory: %w", path, err)
	}
	scratch, err := os.CreateTemp(directory, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("write %s: create scratch: %w", path, err)
	}
	scratchPath := scratch.Name()
	published := false
	defer func() {
		if published {
			return
		}
		if removeErr := removeScratch(scratchPath); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("write %s: remove scratch %s: %w", path, scratchPath, removeErr))
		}
	}()
	if err := scratch.Chmod(mode.Perm()); err != nil {
		_ = scratch.Close()
		return fmt.Errorf("write %s: set mode: %w", path, err)
	}
	if err := fill(scratch); err != nil {
		_ = scratch.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := scratch.Sync(); err != nil {
		_ = scratch.Close()
		return fmt.Errorf("write %s: sync: %w", path, err)
	}
	if err := scratch.Close(); err != nil {
		return fmt.Errorf("write %s: close scratch: %w", path, err)
	}
	if err := os.Rename(scratchPath, path); err != nil {
		return fmt.Errorf("write %s: replace: %w", path, err)
	}
	published = true
	return nil
}

// WriteScratch writes a disposable file in directory and returns its path and
// cleanup. The file is never published over a durable target, so it needs no
// rename or directory sync.
func WriteScratch(directory, pattern string, content []byte) (path string, cleanup func(), err error) {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", nil, fmt.Errorf("create scratch directory %s: %w", directory, err)
	}
	file, err := os.CreateTemp(directory, pattern)
	if err != nil {
		return "", nil, fmt.Errorf("create scratch file: %w", err)
	}
	path = file.Name()
	cleanup = func() { _ = os.Remove(path) }
	if _, err := file.Write(content); err != nil {
		closeErr := file.Close()
		cleanup()
		return "", nil, fmt.Errorf("write scratch file: %w", errors.Join(err, closeErr))
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("close scratch file: %w", err)
	}
	return path, cleanup, nil
}
