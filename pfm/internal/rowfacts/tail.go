package rowfacts

import (
	"bytes"

	claudetranscript "github.com/rezzminator/professor/pfm/internal/transcript"
)

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
		data, whole, err := claudetranscript.ReadTailLines(path, width)
		if err != nil {
			return err
		}
		if scan(data) || whole {
			return nil
		}
	}
	return nil
}
