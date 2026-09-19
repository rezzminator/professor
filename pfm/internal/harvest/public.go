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
		kind := errorKindInvalid
		switch {
		case strings.Contains(err.Error(), "does not exist"):
			kind = errorKindMissing
		case strings.Contains(err.Error(), "not permitted"), strings.Contains(err.Error(), "internal retrieval"):
			kind = errorKindRefused
		case strings.Contains(err.Error(), "cache directory"), strings.Contains(err.Error(), "handle is unavailable"):
			kind = errorKindInternal
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
			if strings.EqualFold(result.Kind, kindArchive) {
				if len(result.Members) > 0 {
					body = h.PublicArchiveListing(source, result.Members)
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
			meta, parsed := parseCacheFrontmatter(string(raw))
			if strings.HasPrefix(string(raw), "---\n") && meta["source"] != frontmatterSourceHarvester &&
				(meta["url"] != "" || meta["method"] != "" || meta["rungs"] != "") {
				return h.publicExportFailure(
					source,
					result,
					"validate cached artifact metadata",
					errors.New("artifact provenance is not a harvester document"),
				)
			}
			if meta["source"] == frontmatterSourceHarvester {
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
		if result.Kind != kindArchiveMember && result.Kind != kindArchive {
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
		if strings.EqualFold(result.Kind, kindArchive) {
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
	out.Tokens = EstimateTokens(body)
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

// PublicArchiveListing is the canonical, redaction-applied archive-listing
// renderer (F19): it wraps formatArchiveListing with the same source
// redaction the file-export path always applied (a fetched URL becomes its
// harvest: handle; a local path becomes "requested archive") so harvestmcp's
// live MCP answer and the exported file never drift again from having two
// renderers. Exported signature: PublicArchiveListing(source string, members
// []Member) string. Output is byte-identical to the pre-export helper this
// replaces -- only its name changed.
func (h *Harvester) PublicArchiveListing(source string, members []Member) string {
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
	case cacheStatusHit, cacheStatusMiss, cacheStatusRefresh:
		return strings.ToLower(strings.TrimSpace(status))
	default:
		return accessPublic
	}
}

func publicKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case kindPDF,
		kindDOCX,
		kindXLSX,
		kindPPTX,
		kindCSV,
		kindJSON,
		kindEPUB,
		kindHTML,
		kindTXT,
		"md",
		kindJPG,
		kindPNG,
		kindGIF,
		kindWebP,
		kindBMP,
		kindTIFF,
		kindSVG,
		kindImage,
		kindZIP,
		kindTAR,
		"7z",
		kindRAR,
		kindArchive,
		kindArchiveMember:
		return strings.ToLower(strings.TrimSpace(kind))
	default:
		return ""
	}
}

func publicBinaryResult(kind, path string, body []byte) bool {
	low := strings.ToLower(strings.TrimSpace(kind))
	if isImageKind(low) || low == kindArchive || low == kindZIP || low == kindTAR || low == "7z" || low == kindRAR {
		return true
	}
	if low == kindArchiveMember {
		return isImageKind(classifyKind(path, "", body)) || isImageKind(SniffMagic(body))
	}
	return false
}

func publicBinaryExtension(kind, path string, body []byte) (string, bool) {
	low := strings.ToLower(strings.TrimSpace(kind))
	byKind := map[string]string{
		kindJPG:  extensionJPG,
		kindPNG:  extensionPNG,
		kindGIF:  extensionGIF,
		kindWebP: extensionWebP,
		kindBMP:  extensionBMP,
		kindTIFF: extensionTIFF,
		kindSVG:  extensionSVG,
		kindZIP:  extensionZIP,
		kindTAR:  extensionTAR,
		kind7Z:   extension7Z,
		kindRAR:  extensionRAR,
	}
	if ext, ok := byKind[low]; ok {
		return ext, true
	}
	if low == kindImage || low == kindArchiveMember || low == kindArchive {
		detected := classifyKind(path, "", body)
		if detected == kindHTML {
			if magic := SniffMagic(body); magic != "" {
				detected = magic
			}
		}
		if detected == kindImage || SniffMagic(body) == kindImage {
			ext := strings.ToLower(filepath.Ext(strings.Split(strings.Split(path, "?")[0], "#")[0]))
			switch ext {
			case extensionJPG, extensionJPEG:
				return extensionJPG, true
			case extensionPNG, extensionGIF, extensionWebP, extensionBMP, extensionTIF, extensionTIFF, extensionSVG:
				if ext == extensionTIF {
					return extensionTIFF, true
				}
				return ext, true
			}
			switch {
			case bytes.HasPrefix(body, []byte("\x89PNG\r\n\x1a\n")):
				return extensionPNG, true
			case bytes.HasPrefix(body, []byte("\xff\xd8\xff")):
				return extensionJPG, true
			case bytes.HasPrefix(body, []byte("GIF87a")), bytes.HasPrefix(body, []byte("GIF89a")):
				return extensionGIF, true
			case len(body) >= 12 && bytes.Equal(body[:4], []byte("RIFF")) && bytes.Equal(body[8:12], []byte("WEBP")):
				return extensionWebP, true
			case bytes.HasPrefix(body, []byte("BM")):
				return extensionBMP, true
			case bytes.HasPrefix(body, []byte("II*\x00")), bytes.HasPrefix(body, []byte("MM\x00*")):
				return extensionTIFF, true
			case bytes.HasPrefix(bytes.TrimSpace(body), []byte("<svg")),
				bytes.HasPrefix(bytes.TrimSpace(body), []byte("<?xml")) && bytes.Contains(body, []byte("<svg")):
				return extensionSVG, true
			}
		}
		for candidate, ext := range map[string]string{kindZIP: extensionZIP, kindTAR: extensionTAR, kind7Z: extension7Z, kindRAR: extensionRAR} {
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
		ErrorKind: errorKindInternal,
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
	if kind == errorKindChallenge {
		out.Challenge = true
	}
	return out
}

func publicErrorKind(result Result) string {
	low := strings.ToLower(strings.TrimSpace(result.ErrorKind))
	switch low {
	case errorKindTimeout, "timed_out", "deadline":
		return errorKindTimeout
	case errorKindDNS, "name_resolution":
		return errorKindDNS
	case errorKindConnect, "connection", "network":
		return errorKindConnect
	case errorKindChallenge, challengeMarkerCloudflare, challengeMarkerCaptcha:
		return errorKindChallenge
	case errorKindBlocked, errorKindRefused, "policy":
		return errorKindRefused
	case errorKindMissing, "not_found", "unresolvable_path":
		return errorKindMissing
	case errorKindConversion, errorKindConvert, "ocr":
		return errorKindConversion
	case errorKindOversized, errorKindTooLarge, "payload_too_large":
		return errorKindOversized
	case errorKindCancelled, "canceled":
		return errorKindCancelled
	case errorKindInvalid, errorKindWrongKind, "unsupported":
		if low == errorKindWrongKind {
			return errorKindWrongKind
		}
		return errorKindInvalid
	case errorKindInternal, "storage", cacheLabel:
		return errorKindInternal
	}
	if result.Challenge {
		return errorKindChallenge
	}
	if result.HTTPStatus == 404 || result.HTTPStatus == 410 {
		return errorKindMissing
	}
	if result.HTTPStatus == 408 || result.HTTPStatus == 504 {
		return errorKindTimeout
	}
	if result.HTTPStatus == 401 || result.HTTPStatus == 403 {
		return errorKindRefused
	}
	err := strings.ToLower(result.Error)
	switch {
	case strings.Contains(err, "context canceled"), strings.Contains(err, "context cancelled"):
		return errorKindCancelled
	case strings.Contains(err, "no such host"), strings.Contains(err, errorKindDNS):
		return errorKindDNS
	case strings.Contains(err, "timed out"), strings.Contains(err, errorKindTimeout):
		return errorKindTimeout
	case strings.Contains(err, "connection refused"),
		strings.Contains(err, "connection reset"),
		strings.Contains(err, "connect:"):
		return errorKindConnect
	case strings.Contains(err, "too large"), strings.Contains(err, "exceeds"), strings.Contains(err, "maximum"):
		return errorKindOversized
	case strings.Contains(err, errorKindConvert), strings.Contains(err, "ocr"):
		return errorKindConversion
	case strings.Contains(err, "not found"), strings.Contains(err, errorKindMissing):
		return errorKindMissing
	case strings.Contains(err, "invalid url"),
		strings.Contains(err, "unsupported url"),
		strings.Contains(err, "source is empty"):
		return errorKindInvalid
	case strings.Contains(err, "findworks"), strings.Contains(err, "find works"), strings.Contains(err, "title — use"):
		return "ambiguous"
	case strings.Contains(err, "fetchimage"),
		strings.Contains(err, "fetch image"),
		strings.Contains(err, "archive tool"),
		strings.Contains(err, "use the `archive`"):
		return errorKindWrongKind
	case strings.Contains(err, cacheLabel), strings.Contains(err, "storage"), strings.Contains(err, "read local file"):
		return errorKindInternal
	}
	return "failed"
}

// PublicFailureMessage returns a safe, actionable diagnostic. It intentionally
// does not include Result.Source or Result.Error: both can contain a provider
// URL, private path, or a provider's internal wording.
func PublicFailureMessage(result Result) string {
	switch publicErrorKind(result) {
	case errorKindTimeout:
		return "The source timed out. Retry later or choose another work."
	case errorKindDNS:
		return "The source could not be resolved. Retry later or choose another work."
	case errorKindConnect:
		return "The connection failed. Retry later or choose another work."
	case errorKindChallenge:
		return "The source is protected by an access challenge. Choose another copy."
	case errorKindRefused:
		return "The request was refused by access policy. Use a public URL or choose another copy."
	case errorKindMissing:
		return "The requested document was not found. Use findWorks, select a result, and fetch it again."
	case errorKindConversion:
		return "The document could not be converted or OCR'd. Try another copy."
	case errorKindOversized:
		return "The document is too large to process. Choose a smaller copy."
	case errorKindCancelled:
		return "The request was cancelled."
	case errorKindInvalid:
		return "The input is invalid. Use findWorks, select a result, and fetch it."
	case "ambiguous":
		return "The title is ambiguous. Use findWorks, select a result, and fetch it."
	case errorKindWrongKind:
		return "This source is an image or archive. Use fetchImage or archive for this media."
	case errorKindInternal:
		return "Harvester could not read or publish its stored result. Retry later."
	default:
		return "Retrieval failed. Retry or choose another work."
	}
}
