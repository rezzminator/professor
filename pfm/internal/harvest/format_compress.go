package harvest

import (
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// compressedDocumentCap bounds what one compressed document may inflate to:
// past it the body is a decompression bomb (or too large to read as one
// document) and ends in a named failure stating the cap.
const (
	compressedDocumentCap      = 64 << 20
	compressedDocumentCapLabel = "64 MiB"
)

// ErrDecompressionBomb is the cap's failure; an Inflater answers it too.
var ErrDecompressionBomb = errors.New("decompresses to more than " + compressedDocumentCapLabel)

var errDecompressionBomb = ErrDecompressionBomb

// compressionSuffixes are stripped from a name to find the inner document's
// own extension (notes.yaml.gz → notes.yaml).
var compressionSuffixes = map[string][]string{
	kindGzip:  {extensionGZ, ".gzip"},
	kindBzip2: {extensionBZ2, ".bzip2"},
	kindZstd:  {".zst", ".zstd"},
	kindXZ:    {extensionXZ},
}

func isCompressedKind(kind string) bool {
	_, ok := compressionSuffixes[kind]
	return ok
}

// decompressDocument inflates one compressed body, at most limit bytes.
// xz has no decoder in the stdlib or the module's dependencies: the
// converter inflates it (Harvester.decompress, format_xz.go).
func decompressDocument(kind string, body []byte, limit int64) ([]byte, error) {
	var reader io.Reader
	switch kind {
	case kindGzip:
		gz, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("gzip header: %w", err)
		}
		defer func() { _ = gz.Close() }()
		reader = gz
	case kindBzip2:
		reader = bzip2.NewReader(bytes.NewReader(body))
	case kindZstd:
		decoder, err := zstd.NewReader(bytes.NewReader(body), zstd.WithDecoderMaxMemory(uint64(limit)+1))
		if err != nil {
			return nil, fmt.Errorf("zstd header: %w", err)
		}
		defer decoder.Close()
		reader = decoder
	default:
		return nil, fmt.Errorf("no decoder for %q", kind)
	}
	inner, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if int64(len(inner)) > limit {
		return inner[:limit], errDecompressionBomb // the head is still usable for a peek
	}
	if err != nil {
		return nil, err
	}
	return inner, nil
}

// isTarHeader reports a tar header block (the ustar magic at offset 257).
func isTarHeader(head []byte) bool {
	return len(head) >= 262 && string(head[257:262]) == "ustar"
}

// innerDocumentName is name without its compression suffix.
func innerDocumentName(kind, name string) string {
	path := stripLocationQuery(name)
	for _, suffix := range compressionSuffixes[kind] {
		if strings.HasSuffix(strings.ToLower(path), suffix) {
			return path[:len(path)-len(suffix)]
		}
	}
	return path
}

// resolveFormat is detectFormat with one compressed layer opened: a
// compressed body whose inner document converts stays formatCompressed; an
// inner file or unsupported kind is named as "<codec>-compressed <type>";
// a bomb, a corrupt stream and a nested compression are refused by name; an
// xz body is opened through the converter when inflate reaches it. inflate
// opens the layer: goInflate, or Harvester.inflater.
func resolveFormat(name string, body []byte, inflate inflateFunc) formatFinding {
	found := detectFormat(name, body)
	if found.class != formatCompressed {
		return found
	}
	codec := found.label
	if found.kind != kindXZ { // xz is peeked whole below: a tar inside is found by its bytes
		if head, _ := decompressDocument(found.kind, body, 512); isTarHeader(head) {
			return finding(formatFileOnly, codec+"-compressed tar archive (application/x-tar)")
		}
	}
	inner, err := inflate(found.kind, body)
	switch {
	case errors.Is(err, errDecompressionBomb):
		return formatFinding{
			class: formatRefused, label: codec + "-compressed file", tooLarge: true,
			reason: "is " + codec + "-compressed and decompresses to more than " + compressedDocumentCapLabel +
				", the harvester's cap for one compressed document (a decompression bomb, or too large to read)",
		}
	case err != nil:
		return formatFinding{
			class: formatRefused, label: codec + "-compressed file",
			reason: "is " + codec + "-compressed but could not be decompressed (" + err.Error() + ")",
		}
	}
	innerFound := detectFormat(innerDocumentName(found.kind, name), inner)
	switch innerFound.class {
	case formatUnknown, formatDocument, formatText:
		return found
	case formatCompressed:
		return formatFinding{
			class: formatRefused, label: codec + "-compressed " + innerFound.label + " stream",
			reason: "is compressed twice (" + codec + " over " + innerFound.label + "); the harvester opens one layer",
		}
	}
	innerFound.label = codec + "-compressed " + innerFound.label
	return innerFound
}

// inflateFunc opens one compressed layer under compressedDocumentCap: Go's own
// decoders (goInflate), or Harvester.inflater, which also reaches the
// converter's xz decoder.
type inflateFunc func(kind string, body []byte) ([]byte, error)

func goInflate(kind string, body []byte) ([]byte, error) {
	return decompressDocument(kind, body, compressedDocumentCap)
}

func (h *Harvester) inflater(ctx context.Context) inflateFunc {
	return func(kind string, body []byte) ([]byte, error) { return h.decompress(ctx, kind, body) }
}

// The compressed-document kinds and the suffixes named after them.
const (
	kindGzip     = "gz"
	kindBzip2    = "bz2"
	kindZstd     = "zst"
	kindXZ       = "xz"
	extensionGZ  = ".gz"
	extensionBZ2 = ".bz2"
	extensionXZ  = ".xz"
)
