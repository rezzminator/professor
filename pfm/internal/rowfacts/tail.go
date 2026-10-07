package rowfacts

import (
	"bytes"
	"fmt"
	"io"
	"os"
)

// readTranscriptTail returns the last up to limit bytes of path, cut to whole lines: a
// tail that starts mid-file drops its first, partial line. whole reports that
// the tail starts at the file's first byte, so there is nothing further back.
func readTranscriptTail(path string, limit int64) (data []byte, whole bool, err error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close %s: %w", path, closeErr)
		}
	}()
	info, err := file.Stat()
	if err != nil {
		return nil, false, fmt.Errorf("stat %s: %w", path, err)
	}
	start := max(0, info.Size()-limit)
	data = make([]byte, info.Size()-start)
	if _, err := file.ReadAt(data, start); err != nil && err != io.EOF {
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	if start > 0 {
		cut := bytes.IndexByte(data, '\n')
		if cut < 0 {
			return nil, false, nil // one line longer than the tail: nothing whole to read
		}
		data = data[cut+1:]
	}
	return data, start == 0, nil
}

// linesBackward calls visit on each non-empty line of data from the last to the
// first, until visit returns true.
func linesBackward(data []byte, visit func(line []byte) (stop bool)) {
	end := len(data)
	for end > 0 {
		start := bytes.LastIndexByte(data[:end], '\n') + 1
		if line := bytes.TrimSpace(data[start:end]); len(line) > 0 && visit(line) {
			return
		}
		end = start - 1
	}
}

// scanTail reads ever larger tails of path — each width in widths, until
// settled reports that the tail answered what was asked, or the file has no
// more to give — and hands each to scan. It returns scan's last verdict.
func scanTail(path string, widths []int64, scan func(data []byte) (settled bool)) error {
	for _, width := range widths {
		data, whole, err := readTranscriptTail(path, width)
		if err != nil {
			return err
		}
		if scan(data) || whole {
			return nil
		}
	}
	return nil
}
