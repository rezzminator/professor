package harvest

// This file is the protocol boundary for results that leave the harvester
// process.  The transport and cache deliberately retain their provenance for
// diagnostics; this layer turns that private receipt into a separate,
// provenance-free artifact before an adapter can return it.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	publicHandlePrefix = "harvest:"
	publicHandleHexLen = sha256.Size * 2
	publicReadLimit    = 50 * 1024 * 1024
	publicSearchLimit  = 10000
)

var (
	publicHandleRE = regexp.MustCompile(`^harvest:[0-9a-f]{64}$`)
	publicBareDOI  = regexp.MustCompile(`(?i)^10\.\d{4,9}/[-._;()/:A-Z0-9]+$`)
	publicPMID     = regexp.MustCompile(`^(?:pmid:)?\d{7,9}$`)
	publicPMCID    = regexp.MustCompile(`(?i)^(?:pmcid:)?pmc\d+$`)
	publicISBN     = regexp.MustCompile(`(?i)^(?:isbn:)?[0-9x][0-9x -]{8,16}$`)
)

type publicHandleRecord struct {
	Target string `json:"target"`
}

// FetchPublic resolves a private opaque handle or a permitted local path,
// fetches through the existing core, and publishes an isolated result.
func (h *Harvester) FetchPublic(ctx context.Context, source string, options FetchOptions) Result {
	resolved, err := h.ResolvePublicSource(source)
	if err != nil {
		log.Printf("harvest: public source resolution failed for %q: %v", source, err)
		kind := "invalid"
		switch {
		case strings.Contains(err.Error(), "does not exist"):
			kind = "missing"
		case strings.Contains(err.Error(), "not permitted"), strings.Contains(err.Error(), "internal retrieval"):
			kind = "refused"
		case strings.Contains(err.Error(), "cache directory"), strings.Contains(err.Error(), "handle is unavailable"):
			kind = "internal"
		}
		return h.PublicResult(
			source,
			Result{Source: source, Error: "public source resolution failed", ErrorKind: kind},
			options.SizeOnly,
		)
	}
	return h.PublicResult(source, h.FetchWithOptions(ctx, resolved, options), options.SizeOnly)
}

// PublicResult publishes a core result without exposing acquisition method,
// rung traces, cache metadata, or private filesystem paths.
func (h *Harvester) PublicResult(source string, result Result, sizeOnly bool) Result {
	if result.Error != "" {
		log.Printf(
			"harvest: public result failure for %q: kind=%q status=%d challenge=%t error=%v",
			source,
			result.ErrorKind,
			result.HTTPStatus,
			result.Challenge,
			result.Error,
		)
		return PublicFailure(source, result)
	}

	out := publicSuccessSkeleton(source, result)
	var body string
	// Generated converter metadata is removed from the public body. Keep its
	// size in the inline budget so stripping a long private Source line cannot
	// turn a clipped core result into an apparently complete one.
	inlineLimit := h.options.MaxInlineChars
	metadataRemoved := 0
	stripPublicMetadata := func(value string) string {
		before := contentChars(value)
		cleaned := stripGeneratedSourceMetadata(value)
		after := contentChars(cleaned)
		if before > after {
			metadataRemoved += before - after
		}
		return cleaned
	}
	if result.Path != "" {
		raw, err := h.readPublicArtifact(result.Path)
		if err != nil {
			return h.publicExportFailure(source, result, "read cached artifact", err)
		}
		if publicBinaryResult(result.Kind, result.Path, raw) {
			ext, ok := publicBinaryExtension(result.Kind, result.Path, raw)
			if !ok {
				return h.publicExportFailure(
					source,
					result,
					"validate binary artifact",
					errors.New("unrecognized binary kind"),
				)
			}
			publicPath, err := h.publicArtifactPath(source, result.Kind, result.Path, ext)
			if err != nil {
				return h.publicExportFailure(source, result, "choose public artifact path", err)
			}
			if err := h.writePublicFile(publicPath, raw); err != nil {
				return h.publicExportFailure(source, result, "write public artifact", err)
			}
			body = result.Content
			if strings.EqualFold(result.Kind, "archive") {
				if len(result.Members) > 0 {
					body = h.publicArchiveListing(source, result.Members)
				} else {
					body = h.rewriteArchiveSource(source, result.Source, body)
				}
			}
			if body != "" {
				body, err = h.rewritePublicImages(source, body, result.Path)
				if err != nil {
					return h.publicExportFailure(source, result, "export embedded image", err)
				}
			}
			out.Path = publicPath
			out.Bytes = int64(len(raw))
		} else {
			fetchedAt := ""
			body = string(raw)
			meta, parsed := parseFrontmatter(string(raw))
			if strings.HasPrefix(string(raw), "---\n") && meta["source"] != "harvester" &&
				(meta["url"] != "" || meta["method"] != "" || meta["rungs"] != "") {
				return h.publicExportFailure(
					source,
					result,
					"validate cached artifact metadata",
					errors.New("artifact provenance is not a harvester document"),
				)
			}
			if meta["source"] == "harvester" {
				body = parsed
				fetchedAt = meta["fetched_at"]
			}
			body = stripPublicMetadata(body)
			body, err = h.rewritePublicImages(source, body, result.Path)
			if err != nil {
				return h.publicExportFailure(source, result, "export embedded image", err)
			}
			publicPath, err := h.publicArtifactPath(source, result.Kind, result.Path, ".md")
			if err != nil {
				return h.publicExportFailure(source, result, "choose public artifact path", err)
			}
			if err := h.writePublicMarkdown(publicPath, body, fetchedAt); err != nil {
				return h.publicExportFailure(source, result, "write public artifact", err)
			}
			out.Path = publicPath
		}
	} else {
		if result.Kind != "archive_member" && result.Kind != "archive" {
			return h.publicExportFailure(
				source,
				result,
				"publish result without complete artifact",
				errors.New("result has no complete artifact path"),
			)
		}
		body = result.Content
		if strings.TrimSpace(body) == "" {
			return h.publicExportFailure(
				source,
				result,
				"publish empty artifact",
				errors.New("successful result has no artifact"),
			)
		}
		if strings.EqualFold(result.Kind, "archive") {
			body = h.rewriteArchiveSource(source, result.Source, body)
		}
		body = stripPublicMetadata(body)
		var err error
		body, err = h.rewritePublicImages(source, body, "")
		if err != nil {
			return h.publicExportFailure(source, result, "export embedded image", err)
		}
		publicPath, err := h.publicArtifactPath(source, result.Kind, "", ".md")
		if err != nil {
			return h.publicExportFailure(source, result, "choose public artifact path", err)
		}
		if err := h.writePublicMarkdown(publicPath, body); err != nil {
			return h.publicExportFailure(source, result, "write public artifact", err)
		}
		out.Path = publicPath
	}

	out.Chars = contentChars(body)
	out.ContentChars = out.Chars
	out.Tokens = estimateTokens(body)
	if metadataRemoved > 0 && inlineLimit > 0 {
		inlineLimit -= metadataRemoved
		if inlineLimit < 1 {
			inlineLimit = 1
		}
	}
	out.Content = truncateInline(body, inlineLimit)
	if sizeOnly {
		out.Content = ""
	}
	info, err := os.Stat(out.Path)
	if err != nil || !info.Mode().IsRegular() {
		if err == nil {
			err = errors.New("public artifact is not a regular file")
		}
		return h.publicExportFailure(source, result, "stat public artifact", err)
	}
	out.Bytes = info.Size()
	return out
}

func (h *Harvester) publicArchiveListing(source string, members []Member) string {
	display := h.publicArchiveDisplay(source)
	return formatArchiveListing(display, members)
}

func (h *Harvester) publicArchiveDisplay(source string) string {
	display := strings.TrimSpace(source)
	if strings.HasPrefix(strings.ToLower(display), "http://") ||
		strings.HasPrefix(strings.ToLower(display), "https://") {
		if handle, err := h.PublicHandle(display); err == nil {
			display = handle
		} else {
			display = "requested archive"
		}
	} else if strings.HasPrefix(display, "/") ||
		strings.HasPrefix(strings.ToLower(display), "file://") {
		display = "requested archive"
	}
	return display
}

func (h *Harvester) rewriteArchiveSource(source, resolved, body string) string {
	if strings.TrimSpace(body) == "" {
		return body
	}
	display := h.publicArchiveDisplay(source)
	if strings.TrimSpace(resolved) != "" {
		body = strings.ReplaceAll(body, resolved, display)
	}
	return body
}

// The Python HTML converter adds a short metadata block before the article.
// Remove only its generated Source field, and only before that block's
// separator; article citations later in the document remain untouched.
func stripGeneratedSourceMetadata(body string) string {
	lines := strings.Split(body, "\n")
	separator := -1
	for index, line := range lines {
		if strings.TrimSpace(line) == "---" {
			separator = index
			break
		}
	}
	if separator < 0 {
		return body
	}
	prefix := make([]string, 0, separator)
	removed := false
	for _, line := range lines[:separator] {
		if strings.HasPrefix(strings.TrimSpace(line), "**Source:**") {
			removed = true
			continue
		}
		prefix = append(prefix, line)
	}
	if !removed {
		return body
	}
	for len(prefix) > 0 && strings.TrimSpace(prefix[len(prefix)-1]) == "" {
		prefix = prefix[:len(prefix)-1]
	}
	suffix := lines[separator+1:]
	for len(suffix) > 0 && strings.TrimSpace(suffix[0]) == "" {
		suffix = suffix[1:]
	}
	return strings.Join(append(prefix, suffix...), "\n")
}

func publicSuccessSkeleton(source string, result Result) Result {
	return Result{
		Source:      source,
		Kind:        publicKind(result.Kind),
		CacheStatus: publicCacheStatus(result.CacheStatus),
		HTTPStatus:  result.HTTPStatus,
		Members:     append([]Member(nil), result.Members...),
	}
}

func publicCacheStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "hit", "miss", "refresh":
		return strings.ToLower(strings.TrimSpace(status))
	default:
		return "public"
	}
}

func publicKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "pdf",
		"docx",
		"xlsx",
		"pptx",
		"csv",
		"json",
		"epub",
		"html",
		"txt",
		"md",
		"jpg",
		"png",
		"gif",
		"webp",
		"bmp",
		"tiff",
		"svg",
		"image",
		"zip",
		"tar",
		"7z",
		"rar",
		"archive",
		"archive_member":
		return strings.ToLower(strings.TrimSpace(kind))
	default:
		return ""
	}
}

func publicBinaryResult(kind, path string, body []byte) bool {
	low := strings.ToLower(strings.TrimSpace(kind))
	if isImageKind(low) || low == "archive" || low == "zip" || low == "tar" || low == "7z" || low == "rar" {
		return true
	}
	if low == "archive_member" {
		return isImageKind(classifyKind(path, "", body)) || isImageKind(SniffMagic(body))
	}
	return false
}

func publicBinaryExtension(kind, path string, body []byte) (string, bool) {
	low := strings.ToLower(strings.TrimSpace(kind))
	byKind := map[string]string{
		"jpg": ".jpg", "png": ".png", "gif": ".gif", "webp": ".webp", "bmp": ".bmp", "tiff": ".tiff", "svg": ".svg",
		"zip": ".zip", "tar": ".tar", "7z": ".7z", "rar": ".rar",
	}
	if ext, ok := byKind[low]; ok {
		return ext, true
	}
	if low == "image" || low == "archive_member" || low == "archive" {
		detected := classifyKind(path, "", body)
		if detected == "html" {
			if magic := SniffMagic(body); magic != "" {
				detected = magic
			}
		}
		if detected == "image" || SniffMagic(body) == "image" {
			ext := strings.ToLower(filepath.Ext(strings.Split(strings.Split(path, "?")[0], "#")[0]))
			switch ext {
			case ".jpg", ".jpeg":
				return ".jpg", true
			case ".png", ".gif", ".webp", ".bmp", ".tif", ".tiff", ".svg":
				if ext == ".tif" {
					return ".tiff", true
				}
				return ext, true
			}
			switch {
			case bytes.HasPrefix(body, []byte("\x89PNG\r\n\x1a\n")):
				return ".png", true
			case bytes.HasPrefix(body, []byte("\xff\xd8\xff")):
				return ".jpg", true
			case bytes.HasPrefix(body, []byte("GIF87a")), bytes.HasPrefix(body, []byte("GIF89a")):
				return ".gif", true
			case len(body) >= 12 && bytes.Equal(body[:4], []byte("RIFF")) && bytes.Equal(body[8:12], []byte("WEBP")):
				return ".webp", true
			case bytes.HasPrefix(body, []byte("BM")):
				return ".bmp", true
			case bytes.HasPrefix(body, []byte("II*\x00")), bytes.HasPrefix(body, []byte("MM\x00*")):
				return ".tiff", true
			case bytes.HasPrefix(bytes.TrimSpace(body), []byte("<svg")),
				bytes.HasPrefix(bytes.TrimSpace(body), []byte("<?xml")) && bytes.Contains(body, []byte("<svg")):
				return ".svg", true
			}
		}
		for candidate, ext := range map[string]string{"zip": ".zip", "tar": ".tar", "7z": ".7z", "rar": ".rar"} {
			if detected == candidate {
				return ext, true
			}
		}
	}
	return "", false
}

func (h *Harvester) publicExportFailure(source string, result Result, operation string, err error) Result {
	log.Printf("harvest: public export %s failed for %q (path=%q): %v", operation, source, result.Path, err)
	failure := Result{
		Source:    source,
		Kind:      publicKind(result.Kind),
		ErrorKind: "internal",
		Error:     "public export failed",
	}
	return PublicFailure(source, failure)
}

// PublicFailure retains only safe failure fields and the caller's input.
// Receipt writers use this boundary when a failure occurs after FetchPublic.
func PublicFailure(source string, result Result) Result {
	kind := publicErrorKind(result)
	out := Result{Source: source, Error: PublicFailureMessage(result), ErrorKind: kind}
	if result.HTTPStatus >= 400 && result.HTTPStatus < 600 {
		out.HTTPStatus = result.HTTPStatus
	}
	if kind == "challenge" {
		out.Challenge = true
	}
	return out
}

func publicErrorKind(result Result) string {
	low := strings.ToLower(strings.TrimSpace(result.ErrorKind))
	switch low {
	case "timeout", "timed_out", "deadline":
		return "timeout"
	case "dns", "name_resolution":
		return "dns"
	case "connect", "connection", "network":
		return "connect"
	case "challenge", "cloudflare", "captcha":
		return "challenge"
	case "blocked", "refused", "policy":
		return "refused"
	case "missing", "not_found", "unresolvable_path":
		return "missing"
	case "conversion", "convert", "ocr":
		return "conversion"
	case "oversized", "too_large", "payload_too_large":
		return "oversized"
	case "cancelled", "canceled":
		return "cancelled"
	case "invalid", "wrong_kind", "unsupported":
		if low == "wrong_kind" {
			return "wrong_kind"
		}
		return "invalid"
	case "internal", "storage", "cache":
		return "internal"
	}
	if result.Challenge {
		return "challenge"
	}
	if result.HTTPStatus == 404 || result.HTTPStatus == 410 {
		return "missing"
	}
	if result.HTTPStatus == 408 || result.HTTPStatus == 504 {
		return "timeout"
	}
	if result.HTTPStatus == 401 || result.HTTPStatus == 403 {
		return "refused"
	}
	err := strings.ToLower(result.Error)
	switch {
	case strings.Contains(err, "context canceled"), strings.Contains(err, "context cancelled"):
		return "cancelled"
	case strings.Contains(err, "no such host"), strings.Contains(err, "dns"):
		return "dns"
	case strings.Contains(err, "timed out"), strings.Contains(err, "timeout"):
		return "timeout"
	case strings.Contains(err, "connection refused"),
		strings.Contains(err, "connection reset"),
		strings.Contains(err, "connect:"):
		return "connect"
	case strings.Contains(err, "too large"), strings.Contains(err, "exceeds"), strings.Contains(err, "maximum"):
		return "oversized"
	case strings.Contains(err, "convert"), strings.Contains(err, "ocr"):
		return "conversion"
	case strings.Contains(err, "not found"), strings.Contains(err, "missing"):
		return "missing"
	case strings.Contains(err, "invalid url"),
		strings.Contains(err, "unsupported url"),
		strings.Contains(err, "source is empty"):
		return "invalid"
	case strings.Contains(err, "findworks"), strings.Contains(err, "find works"), strings.Contains(err, "title — use"):
		return "ambiguous"
	case strings.Contains(err, "fetchimage"),
		strings.Contains(err, "fetch image"),
		strings.Contains(err, "archive tool"),
		strings.Contains(err, "use the `archive`"):
		return "wrong_kind"
	case strings.Contains(err, "cache"), strings.Contains(err, "storage"), strings.Contains(err, "read local file"):
		return "internal"
	}
	return "failed"
}

// PublicFailureMessage returns a safe, actionable diagnostic. It intentionally
// does not include Result.Source or Result.Error: both can contain a provider
// URL, private path, or a provider's internal wording.
func PublicFailureMessage(result Result) string {
	switch publicErrorKind(result) {
	case "timeout":
		return "The source timed out. Retry later or choose another work."
	case "dns":
		return "The source could not be resolved. Retry later or choose another work."
	case "connect":
		return "The connection failed. Retry later or choose another work."
	case "challenge":
		return "The source is protected by an access challenge. Choose another copy."
	case "refused":
		return "The request was refused by access policy. Use a public URL or choose another copy."
	case "missing":
		return "The requested document was not found. Use findWorks, select a result, and fetch it again."
	case "conversion":
		return "The document could not be converted or OCR'd. Try another copy."
	case "oversized":
		return "The document is too large to process. Choose a smaller copy."
	case "cancelled":
		return "The request was cancelled."
	case "invalid":
		return "The input is invalid. Use findWorks, select a result, and fetch it."
	case "ambiguous":
		return "The title is ambiguous. Use findWorks, select a result, and fetch it."
	case "wrong_kind":
		return "This source is an image or archive. Use fetchImage or archive for this media."
	case "internal":
		return "Harvester could not read or publish its stored result. Retry later."
	default:
		return "Retrieval failed. Retry or choose another work."
	}
}
