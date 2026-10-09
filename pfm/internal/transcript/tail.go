package transcript

import (
	"bytes"
	"fmt"
	"io"
	"os"
)

// ReadTailLines returns the last up to limit bytes of path, cut to whole lines: a
// tail that starts mid-file drops its first, partial line. whole reports that
// the tail starts at the file's first byte, so there is nothing further back.
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
