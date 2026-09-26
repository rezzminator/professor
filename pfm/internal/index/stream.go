package index

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
)

func readCompleteLines(
	path string,
	start int64,
	handle func([]byte),
) (parsedOffset, bytesRead int64, err error) {
	file, err := os.Open(path)
	if err != nil {
		return start, 0, fmt.Errorf("open %q: %w", path, err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close %q: %w", path, closeErr))
		}
	}()

	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return start, 0, fmt.Errorf("seek %q to %d: %w", path, start, err)
	}

	parsedOffset = start
	// Transcript records are commonly tens of KiB. ReadSlice lets those rows
	// borrow the reader's storage instead of allocating a new slice for every
	// record; only an unusually large record spills into the reusable buffer.
	// On a long transcript this keeps peak memory tied to the largest record,
	// not to allocator churn across the size of the file.
	reader := bufio.NewReaderSize(file, 64<<10)
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
		if len(lineBytes) != 0 && lineBytes[len(lineBytes)-1] == '\n' {
			handle(lineBytes)
			parsedOffset += int64(len(lineBytes))
		}
		overflow.Reset()

		switch {
		case readErr == nil:
			continue
		case errors.Is(readErr, io.EOF):
			return parsedOffset, bytesRead, nil
		default:
			return parsedOffset, bytesRead, fmt.Errorf("read %q: %w", path, readErr)
		}
	}
}
