package harvest

import (
	"context"
	"errors"
	"fmt"
)

// Inflater opens one compressed document whose codec the Go module carries
// no decoder for — xz, which the converter decompresses with Python's stdlib
// lzma — at most limit bytes; past it the answer wraps ErrDecompressionBomb.
// Optional: without it an xz document is named as unreadable, never guessed.
type Inflater interface {
	Inflate(ctx context.Context, codec string, body []byte, limit int64) ([]byte, error)
}

// decompress inflates one compressed document under compressedDocumentCap:
// gzip, bzip2 and zstd in Go, xz through the converter (Inflater).
func (h *Harvester) decompress(ctx context.Context, kind string, body []byte) ([]byte, error) {
	var inner []byte
	var err error
	if kind == kindXZ {
		inflater, ok := h.options.Converter.(Inflater)
		if !ok {
			return nil, errors.New(
				"no xz decoder is configured: the converter that carries one (Python's lzma) is not available",
			)
		}
		inner, err = inflater.Inflate(ctx, kindXZ, body, compressedDocumentCap)
	} else {
		inner, err = decompressDocument(kind, body, compressedDocumentCap)
	}
	if errors.Is(err, ErrDecompressionBomb) {
		return nil, fmt.Errorf(
			"%w, the harvester's cap for one compressed document (a decompression bomb, or too large to read)",
			ErrDecompressionBomb,
		)
	}
	return inner, err
}

// innerDocumentRefusal names an inflated body that is not a document — a
// file-only, dropped or refused kind found by its bytes — nil for a document.
// An xz body is only opened at conversion, so this is where its inner type is
// first seen.
func innerDocumentRefusal(kind, innerSource string, inner []byte) error {
	found := detectFormat(innerSource, inner)
	switch found.class {
	case formatFileOnly, formatDropped, formatRefused:
		return fmt.Errorf(
			"%s-compressed document holds a %s, not a document the harvester reads; download it with `download`",
			kind,
			found.label,
		)
	}
	return nil
}
