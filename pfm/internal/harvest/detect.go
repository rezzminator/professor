package harvest

import (
	"bytes"
	"path/filepath"
	"strings"
)

var detectImageExts = map[string]struct{}{
	".jpg": {}, ".jpeg": {}, ".png": {}, ".gif": {}, ".webp": {},
	".bmp": {}, ".tif": {}, ".tiff": {}, ".svg": {},
}

var detectExtKinds = map[string]string{
	".pdf": "pdf", ".docx": "docx", ".xlsx": "xlsx", ".pptx": "pptx",
	".csv": "csv", ".json": "json", ".epub": "epub", ".zip": "zip", ".7z": "7z",
	".rar": "rar", ".htm": "html", ".html": "html",
}

// epubContentMarker: an EPUB is a ZIP whose FIRST, UNCOMPRESSED member is a
// `mimetype` file holding exactly `application/epub+zip`, so those bytes sit in
// the first ~60 bytes of the file. This separates an EPUB (a book to convert)
// from a plain zip (an archive to browse) when both arrive as PK\x03\x04.
const epubContentMarker = "application/epub+zip"

// LooksLikeEpub mirrors detect.looks_like_epub.
func LooksLikeEpub(body []byte) bool {
	head := body
	if len(head) > 200 {
		head = head[:200]
	}
	return strings.Contains(string(head), epubContentMarker)
}

// DetectKind mirrors detect.detect_kind: source names are only a hint and an
// unknown extension is deliberately treated as HTML. Compound compression
// suffixes must be checked before filepath.Ext would reduce them to .gz/.bz2.
func DetectKind(source string) string {
	name := strings.ToLower(strings.TrimRight(stripLocationQuery(source), "/"))
	for _, suffix := range []string{".tar.gz", ".tgz", ".tar.bz2", ".tbz2", ".tar.xz", ".txz", ".tar"} {
		if strings.HasSuffix(name, suffix) {
			return "tar"
		}
	}
	ext := strings.ToLower(filepath.Ext(name))
	if kind, ok := detectExtKinds[ext]; ok {
		return kind
	}
	if _, ok := detectImageExts[ext]; ok {
		return "image"
	}
	if ext == ".gz" || ext == ".bz2" || ext == ".xz" {
		return "tar"
	}
	return "html"
}

// SniffKind mirrors detect._sniff_kind. Empty string means the response is
// explicitly HTML/text or has no recognizable non-text format.
func SniffKind(contentType string, head []byte) string {
	ct := baseContentType(contentType)
	switch {
	case ct == "application/pdf":
		return "pdf"
	case ct == "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return "docx"
	case ct == "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return "xlsx"
	case ct == "application/vnd.openxmlformats-officedocument.presentationml.presentation":
		return "pptx"
	case ct == "application/epub+zip":
		return "epub"
	case ct == "application/zip", ct == "application/x-zip-compressed", ct == "application/x-zip":
		return "zip"
	case ct == "application/x-7z-compressed":
		return "7z"
	case ct == "application/x-rar-compressed", ct == "application/vnd.rar":
		return "rar"
	case ct == "application/x-tar",
		ct == "application/gzip",
		ct == "application/x-gzip",
		ct == "application/x-bzip2",
		ct == "application/x-xz":
		return "tar"
	case strings.Contains(ct, "openxmlformats-officedocument"):
		switch {
		case strings.Contains(ct, "wordprocessingml"):
			return "docx"
		case strings.Contains(ct, "spreadsheetml"):
			return "xlsx"
		case strings.Contains(ct, "presentationml"):
			return "pptx"
		default:
			return "zip"
		}
	case strings.HasPrefix(ct, "image/"):
		return "image"
	case ct == "application/json", ct == "text/json", ct == "application/ld+json":
		return "json"
	case ct == "text/csv", ct == "application/csv":
		return "csv"
	case ct == "text/html",
		ct == "text/plain",
		ct == "application/xhtml+xml",
		ct == "application/xml",
		ct == "text/xml",
		ct == "text/markdown":
		return ""
	default:
		return SniffMagic(head)
	}
}

// SniffMagic mirrors detect.sniff_magic. Images intentionally collapse to the
// generic image kind here; the transport classifier can refine by extension or
// exact image content type when it needs a cache suffix.
func SniffMagic(head []byte) string {
	switch {
	case bytes.HasPrefix(head, []byte("%PDF-")):
		return "pdf"
	case bytes.HasPrefix(head, []byte("PK\x03\x04")):
		return "zip"
	case bytes.HasPrefix(head, []byte("7z\xbc\xaf\x27\x1c")):
		return "7z"
	case bytes.HasPrefix(head, []byte("Rar!\x1a\x07")):
		return "rar"
	case bytes.HasPrefix(head, []byte("\x1f\x8b")),
		bytes.HasPrefix(head, []byte("BZh")),
		bytes.HasPrefix(head, []byte("\xfd7zXZ\x00")):
		return "tar"
	case bytes.HasPrefix(head, []byte("\xff\xd8\xff")),
		bytes.HasPrefix(head, []byte("\x89PNG\r\n\x1a\n")),
		bytes.HasPrefix(head, []byte("GIF87a")),
		bytes.HasPrefix(head, []byte("GIF89a")):
		return "image"
	case len(head) >= 12 && bytes.Equal(head[:4], []byte("RIFF")) && bytes.Equal(head[8:12], []byte("WEBP")):
		return "image"
	}
	return ""
}

func IsPlainText(name, contentType, sample string) bool {
	ct := baseContentType(contentType)
	if ct == "text/html" || ct == "application/xhtml+xml" || ct == "application/xml" || ct == "text/xml" {
		return false
	}
	ext := strings.ToLower(filepath.Ext(strings.TrimRight(stripLocationQuery(name), "/")))
	if ext == ".html" || ext == ".htm" {
		return false
	}
	if ct == "text/plain" || ct == "text/markdown" || ct == "text/x-markdown" || ct == "text/x-rst" ||
		ext == ".txt" || ext == ".text" || ext == ".md" || ext == ".markdown" || ext == ".rst" || ext == ".log" || ext == ".tex" || ext == ".org" {
		return !looksHTML(sample)
	}
	return strings.TrimSpace(sample) != "" && !looksHTML(sample)
}

func stripLocationQuery(value string) string {
	if i := strings.IndexAny(value, "?#"); i >= 0 {
		return value[:i]
	}
	return value
}

func baseContentType(value string) string {
	return strings.ToLower(strings.TrimSpace(strings.SplitN(value, ";", 2)[0]))
}

// imageExtension is used by media cache naming and follows detect.image_ext.
func imageExtension(location, fallback string) string {
	ext := strings.ToLower(filepath.Ext(stripLocationQuery(location)))
	if _, ok := detectImageExts[ext]; ok {
		return ext
	}
	return fallback
}

func looksHTML(sample string) bool {
	low := strings.ToLower(sample)
	for _, m := range []string{"<html", "<!doctype html", "<head", "<body", "<div", "<table", "<article", "<section", "<span", "<p>", "<p "} {
		if strings.Contains(low, m) {
			return true
		}
	}
	return false
}
func hasMagic(body []byte, magic ...byte) bool { return bytes.HasPrefix(body, magic) }
