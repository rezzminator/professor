package transcript

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"sync/atomic"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/gather"
)

// wholeReadObserver, when set, is told the path of every ReadLines and
// ReadMeta pass, and of every ResumeLines pass that starts from the top: the
// reads whose cost grows with the transcript. It is a test
// seam for the status line's read-once law and is never set in production.
var wholeReadObserver atomic.Pointer[func(path string)]

// ObserveWholeReads installs fn as the observer of whole-transcript reads and
// returns the function that removes it.
func ObserveWholeReads(fn func(path string)) (restore func()) {
	wholeReadObserver.Store(&fn)
	return func() { wholeReadObserver.Store(nil) }
}

func noteWholeRead(path string) {
	if fn := wholeReadObserver.Load(); fn != nil {
		(*fn)(path)
	}
}

// OpenError is ReadLines failing to open its file: nothing was read.
type OpenError struct {
	Path string
	Err  error
}

func (e *OpenError) Error() string { return fmt.Sprintf("open %q: %v", e.Path, e.Err) }

func (e *OpenError) Unwrap() error { return e.Err }

// ReadLines streams path from byte offset start, handing each line to handle
// without its newline. complete is false only for a last line the file does
// not end with a newline (its writer may be mid-append); a caller that resumes
// from parsedOffset skips it. line is valid only during the call: a line
// longer than the read buffer spills into one reused buffer, so peak memory
// follows the largest record, never the file. parsedOffset is start plus every
// complete line's bytes, bytesRead every byte read.
func ReadLines(
	path string,
	start int64,
	handle func(line []byte, complete bool),
) (parsedOffset, bytesRead int64, err error) {
	noteWholeRead(path)
	file, err := os.Open(path)
	if err != nil {
		return start, 0, &OpenError{Path: path, Err: err}
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close %q: %w", path, closeErr))
		}
	}()
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return start, 0, fmt.Errorf("seek %q to %d: %w", path, start, err)
	}
	parsedOffset, bytesRead, err = ScanLines(file, start, handle)
	if err != nil {
		return parsedOffset, bytesRead, fmt.Errorf("read %q: %w", path, err)
	}
	return parsedOffset, bytesRead, nil
}

// ScanLines is ReadLines over a reader already positioned at byte offset
// start, for a caller that holds the file open itself; its error is the
// reader's own.
func ScanLines(
	r io.Reader,
	start int64,
	handle func(line []byte, complete bool),
) (parsedOffset, bytesRead int64, err error) {
	return scanLinesUntil(r, start, nil, handle)
}

// scanLinesUntil is ScanLines that asks spent (nil: never) before each line and,
// once it answers true, stops there with errReadBudget: a line already begun
// is read to its end first, so a stop always falls on a line boundary and a
// line longer than one budget's read is still folded, once.
func scanLinesUntil(
	r io.Reader,
	start int64,
	spent func() bool,
	handle func(line []byte, complete bool),
) (parsedOffset, bytesRead int64, err error) {
	parsedOffset = start
	reader := bufio.NewReaderSize(r, 64<<10)
	var overflow bytes.Buffer
	for {
		if spent != nil && overflow.Len() == 0 && spent() {
			return parsedOffset, bytesRead, errReadBudget
		}
		line, readErr := reader.ReadSlice('\n')
		bytesRead += int64(len(line))
		if errors.Is(readErr, bufio.ErrBufferFull) {
			_, _ = overflow.Write(line)
			continue
		}
		lineBytes := line
		if overflow.Len() != 0 {
			_, _ = overflow.Write(line)
			lineBytes = overflow.Bytes()
		}
		if n := len(lineBytes); n != 0 && lineBytes[n-1] == '\n' {
			handle(lineBytes[:n-1], true)
			parsedOffset += int64(n)
		} else if n != 0 && errors.Is(readErr, io.EOF) {
			handle(lineBytes, false)
		}
		overflow.Reset()
		switch {
		case readErr == nil:
			continue
		case errors.Is(readErr, io.EOF):
			return parsedOffset, bytesRead, nil
		default:
			return parsedOffset, bytesRead, readErr
		}
	}
}

// Resume is where a fold of a transcript's whole lines stopped, in a form a
// caller can keep between processes: the file's identity (device and inode,
// zero where the platform names none), the end of the last whole line folded,
// and the bytes just before it (at most anchorBytes, the Reader's anchor).
type Resume struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
	Offset int64  `json:"offset"`
	Anchor []byte `json:"anchor"`
}

// errReadBudget stops a ResumeLines read at its deadline.
var errReadBudget = errors.New("read budget spent")

// budgetNow is the wall clock a ResumeLines deadline is read against; a test
// seam, never set in production.
var budgetNow = clock.Real.Now

// ResumeLines is ReadLines resumed from a fold its caller kept, and bounded by
// deadline. It starts at from.Offset only while from still describes the file:
// the same device and inode, a size no smaller than the offset, and the bytes
// before the offset equal to from.Anchor; anything else — shrunk, replaced,
// rewritten, a zero from — starts at 0. begin is told the start before the
// first line, so the caller resets its fold on 0. handle gets each line as
// ReadLines hands it, a torn final line with complete false. A zero deadline
// reads to the end; once it passes, the read stops at the next line boundary
// (a line already begun is read whole) and done is false. next is where the following read resumes: the end of the last whole
// line handed over, a torn final line left past it.
func ResumeLines(
	path string,
	from Resume,
	deadline time.Time,
	begin func(start int64),
	handle func(line []byte, complete bool),
) (next Resume, done bool, err error) {
	file, err := os.Open(path)
	if err != nil {
		return Resume{}, false, &OpenError{Path: path, Err: err}
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close %q: %w", path, closeErr))
		}
	}()
	info, err := file.Stat()
	if err != nil {
		return Resume{}, false, fmt.Errorf("stat %q: %w", path, err)
	}
	id, err := gather.FileIDOfInfo(info)
	if err != nil {
		return Resume{}, false, fmt.Errorf("identify %q: %w", path, err)
	}
	next.Device, next.Inode = id.Device, id.Inode
	start := int64(0)
	if from.Offset > 0 && from.Device == next.Device && from.Inode == next.Inode && info.Size() >= from.Offset {
		held, err := anchorAt(file, from.Offset)
		if err != nil && !errors.Is(err, io.EOF) { // EOF: shrunk since the stat, so read from the top
			return Resume{}, false, fmt.Errorf("read %q: %w", path, err)
		}
		if err == nil && bytes.Equal(held, from.Anchor) {
			start = from.Offset
		}
	}
	begin(start)
	if start == 0 {
		noteWholeRead(path)
	}
	if start >= info.Size() {
		deadline = time.Time{} // nothing new to read: no budget can cut it short
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return Resume{}, false, fmt.Errorf("seek %q to %d: %w", path, start, err)
	}
	spent := func() bool { return !deadline.IsZero() && budgetNow().After(deadline) }
	parsed, _, err := scanLinesUntil(file, start, spent, handle)
	switch {
	case errors.Is(err, errReadBudget):
	case err != nil:
		return Resume{}, false, fmt.Errorf("read %q: %w", path, err)
	default:
		done = true
	}
	next.Offset = parsed
	if next.Anchor, err = anchorAt(file, parsed); err != nil {
		return Resume{}, false, fmt.Errorf("read %q: %w", path, err)
	}
	return next, done, nil
}

// anchorAt is the bytes just before offset, at most anchorBytes of them.
func anchorAt(file *os.File, offset int64) ([]byte, error) {
	held := make([]byte, min(offset, anchorBytes))
	if _, err := file.ReadAt(held, offset-int64(len(held))); err != nil {
		return nil, err
	}
	return held, nil
}
