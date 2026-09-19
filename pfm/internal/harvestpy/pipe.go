package harvestpy

import (
	"bufio"
	"errors"
	"fmt"
)

// ErrSidecarResponseTooLarge is a sidecar response line that ran past its
// budget without ever ending. Every other input in the harvester is capped;
// an unbounded bufio read on a subprocess pipe is one runaway worker away from
// the whole daemon's memory, and it fails as a NAMED error rather than as an
// out-of-memory kill nobody can attribute.
var ErrSidecarResponseTooLarge = errors.New("harvestpy sidecar response exceeded its size limit")

// converterResponseLimit bounds one conversion response line. The largest
// legitimate one is a converted document: harvest caps an archive member at
// 100 MiB (internal/harvest/archive.go MaxArchiveFileBytes), and the markdown
// extracted from a document that size is a small fraction of it, JSON escaping
// included — 64 MiB is far above any real conversion and far below a runaway.
//
// browserResponseLimit bounds one rendered page: 32 MiB of HTML in a single
// JSON line is already an order of magnitude past the largest page the ladder
// has met.
//
// Both are vars so a test can shrink them; production never rewrites them.
var (
	converterResponseLimit = 64 << 20
	browserResponseLimit   = 32 << 20
)

// readLineBounded reads one newline-terminated protocol line, refusing at
// limit bytes. It keeps bufio.Reader.ReadBytes' contract otherwise: the bytes
// read so far travel back with a read error (EOF included), so a caller can
// still report what the worker managed to say.
func readLineBounded(reader *bufio.Reader, limit int) ([]byte, error) {
	line := make([]byte, 0, 4096)
	for {
		chunk, err := reader.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > limit {
			return nil, fmt.Errorf("%w: over %d bytes with no end of line", ErrSidecarResponseTooLarge, limit)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return line, err
	}
}
