package transcript

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
)

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
	parsedOffset = start
	reader := bufio.NewReaderSize(r, 64<<10)
	var overflow bytes.Buffer
	for {
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
