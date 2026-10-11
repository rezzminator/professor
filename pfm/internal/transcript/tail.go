package transcript

import (
	"bytes"
	"fmt"
	"io"
	"os"
)

// maxTailLine bounds how far ReadTailLines reads back to reach the start of a
// last line longer than its window.
const maxTailLine = 32 << 20

// tailChunk is how much ReadTailLines reads per step while it looks for the
// start of a last line longer than its window.
const tailChunk = 64 << 10

// ReadTailLines returns the last up to limit bytes of path, cut to whole lines: a
// tail that starts mid-file drops its first, partial line. whole reports that
// the tail starts at the file's first byte, so there is nothing further back.
// A last line longer than limit is read further: the tail is that whole line
// with limit bytes of whole lines before it, so the lines a caller came for are
// not hidden behind one very large entry. A last line past maxTailLine is an
// error that says so, never an empty tail.
func ReadTailLines(path string, limit int64) (data []byte, whole bool, err error) {
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
	size := info.Size()
	start := max(0, size-limit)
	data, err = readRange(file, path, start, size)
	if err != nil {
		return nil, false, err
	}
	if start == 0 {
		return data, true, nil
	}
	if bytes.IndexByte(bytes.TrimSuffix(data, []byte{'\n'}), '\n') < 0 {
		// The window lies inside the last line: find where that line starts.
		lastStart, err := lastLineStart(file, path, start, size)
		if err != nil {
			return nil, false, err
		}
		// One byte more than limit, so the cut below drops a line before the
		// last one and never the last one itself.
		start = max(0, lastStart-limit-1)
		if data, err = readRange(file, path, start, size); err != nil {
			return nil, false, err
		}
		if start == 0 {
			return data, true, nil
		}
	}
	return data[bytes.IndexByte(data, '\n')+1:], false, nil
}

// readRange reads path's bytes from start to end.
func readRange(file *os.File, path string, start, end int64) ([]byte, error) {
	data := make([]byte, end-start)
	if _, err := file.ReadAt(data, start); err != nil && err != io.EOF {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return data, nil
}

// lastLineStart is the offset where the last line of the size-byte file begins,
// found by reading back in chunks from before: the last line is known to reach
// at least that far. A line longer than maxTailLine is an error.
func lastLineStart(file *os.File, path string, before, size int64) (int64, error) {
	lastStart := int64(0)
	for end := before; end > 0 && size-end <= maxTailLine; {
		begin := max(0, end-tailChunk)
		chunk, err := readRange(file, path, begin, end)
		if err != nil {
			return 0, err
		}
		if at := bytes.LastIndexByte(chunk, '\n'); at >= 0 {
			lastStart = begin + int64(at) + 1
			break
		}
		end = begin
	}
	if size-lastStart > maxTailLine {
		return 0, fmt.Errorf("last line of %s is longer than %d bytes", path, int64(maxTailLine))
	}
	return lastStart, nil
}
