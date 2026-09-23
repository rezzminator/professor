package harvest

import (
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
)

// The failure classes a public result names beyond the transport kinds in
// types.go. Each is one line of the public failure table (publicFailureTable).
const (
	errorKindUnsupported = "unsupported"
	errorKindTLS         = "tls"
	errorKindForbidden   = "forbidden"
	errorKindRateLimited = "rate_limited"
	errorKindServer      = "server_error"
	errorKindEmpty       = "empty"
	errorKindAppShell    = "app_shell"
	errorKindNoOpenCopy  = "no_open_copy"
	errorKindLogin       = "login"
	errorKindPaywall     = "paywall"
)

// localEmptyFileText names a zero-byte local file: nothing to convert, and
// no other copy or retry to suggest.
const localEmptyFileText = "The file is empty (0 bytes): there is nothing to read. It exists at its path, unchanged."

// anotherCopy is the next step every class that a different copy can cure
// names, in the current tools only; anotherCopyLead opens a sentence with it.
const (
	anotherCopyRest = "another copy at another URL (webSearch, when configured) and read it with readPage, " +
		"or with findWorks and readWork if it is a scholarly work"
	anotherCopy     = "find " + anotherCopyRest
	anotherCopyLead = "Find " + anotherCopyRest
)

// failureStatusKind classifies an HTTP status the result carries without a
// named kind; "" when the status names no class.
func failureStatusKind(status int) string {
	switch {
	case status == http.StatusNotFound || status == http.StatusGone:
		return errorKindMissing
	case status == http.StatusRequestTimeout || status == http.StatusGatewayTimeout:
		return errorKindTimeout
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return errorKindForbidden
	case status == http.StatusTooManyRequests:
		return errorKindRateLimited
	case status >= 500 && status < 600:
		return errorKindServer
	}
	return ""
}

// failureTextKind classifies an unnamed failure by the wording the core's
// own error builders use; "" when the text names no class.
func failureTextKind(err string) string {
	switch {
	case strings.Contains(err, "x509:"), strings.Contains(err, "tls:"), strings.Contains(err, "certificate"):
		return errorKindTLS
	case strings.Contains(err, "login wall"), strings.Contains(err, "sign-in"), strings.Contains(err, "sign in"):
		return errorKindLogin
	case strings.Contains(err, "open-access"), strings.Contains(err, "oa chain exhausted"):
		return errorKindNoOpenCopy
	case strings.Contains(err, "paywall"):
		return errorKindPaywall
	case strings.Contains(err, "no usable content"), strings.Contains(err, "no readable content"),
		strings.Contains(err, "empty page"), strings.Contains(err, "empty body"):
		return errorKindEmpty
	case strings.Contains(err, "app shell"):
		return errorKindAppShell
	case strings.Contains(err, "is disabled"):
		return errorKindDisabled
	}
	return ""
}

// browserRan reports whether the real-browser rung was among the rungs tried.
func browserRan(rungs []string) bool {
	for _, rung := range rungs {
		if rung == "browser" || strings.HasPrefix(rung, "browser-") {
			return true
		}
	}
	return false
}

// rungsRunNote names the rungs that were tried, by public class: a provider or
// mirror name never reaches a caller.
func rungsRunNote(rungs []string) string {
	public := make([]string, 0, len(rungs))
	for _, rung := range rungs {
		name := rung
		if !strings.HasPrefix(rung, "oa:") {
			name = PublicMethod(rung)
		}
		if len(public) > 0 && public[len(public)-1] == name {
			continue
		}
		public = append(public, name)
	}
	if len(public) == 0 {
		return ""
	}
	return " Rungs tried: " + rungsPhrase(public) + "."
}

var (
	failureURLPattern  = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://\S+`)
	failurePathPattern = regexp.MustCompile(`(^|[\s"'=(])(/[^\s"')]+)`)
	failureIPPattern   = regexp.MustCompile(`\b\d{1,3}(\.\d{1,3}){3}(:\d+)?\b`)
)

// redactFailureText keeps an unclassified error's own wording for the caller
// while removing what may identify a provider or a private file: URLs,
// absolute paths and addresses.
func redactFailureText(text string) string {
	text = failureURLPattern.ReplaceAllString(text, "<url>")
	text = failurePathPattern.ReplaceAllString(text, "$1<path>")
	text = failureIPPattern.ReplaceAllString(text, "<address>")
	text = strings.Join(strings.Fields(text), " ")
	if runes := []rune(text); len(runes) > 300 {
		text = string(runes[:300]) + "…"
	}
	return strings.TrimRight(text, ". ")
}

const (
	// converterFailedMarker opens every harvestpy ok:false answer as
	// harvestpy's converterFailure wraps it: "harvestpy conversion failed
	// (<class>): <message> (stderr: <tail>)".
	converterFailedMarker = "harvestpy conversion failed ("
	// converterFailureLead opens the public text of a converter's own failure.
	converterFailureLead = "The document was retrieved but the converter could not read it: "
	// unclassifiedLead opens the public text of a failure no class names.
	unclassifiedLead = "Retrieval failed for a reason the harvester could not classify: "
	// errorKindUnclassified is the kind of a failure no class names.
	errorKindUnclassified = "failed"
)

// converterNamedFailure is the converter's own words in a harvestpy ok:false
// error — without the exception class and the stderr tail — redacted as an
// unclassified error is; empty when text is not a converter failure.
func converterNamedFailure(text string) string {
	_, rest, ok := strings.Cut(text, converterFailedMarker)
	if !ok {
		return ""
	}
	class, message, ok := strings.Cut(rest, "): ")
	if !ok {
		return ""
	}
	if end := strings.LastIndex(message, " (stderr: "); end >= 0 {
		message = message[:end]
	}
	return redactFailureText(strings.TrimPrefix(message, class+": "))
}

func isLocalFailureSource(source string) bool {
	return strings.HasPrefix(strings.ToLower(source), "file://") || filepath.IsAbs(source)
}

// publicFailureTable is the public failure table: the named cause, the rungs that
// ran, and what the caller can do. Result.Source and Result.Error are never
// quoted except through redactFailureText for an unclassified error.
func publicFailureTable(result Result, kind string) string {
	rungs := rungsRunNote(result.Rungs)
	status := result.HTTPStatus
	switch kind {
	case errorKindTimeout:
		return "The source timed out: it did not answer in time." + rungs + " Retry later; if it keeps timing out, " + anotherCopy + "."
	case errorKindDNS:
		return "The source's host name does not resolve (DNS lookup failed: no such host)." + rungs +
			" Check the URL for a typo; if it is right, the site is down or gone — " + anotherCopy + "."
	case errorKindTLS:
		return "The secure connection to the source failed (TLS handshake or certificate error)." + rungs +
			" The harvester never skips certificate checks; " + anotherCopy + "."
	case errorKindConnect:
		return "The connection failed: the source refused, reset or could not be reached." + rungs + " Retry later, or " + anotherCopy + "."
	case errorKindChallenge:
		// No vendor is named: the result carries nothing the site sent (headers,
		// cookies, markup), and the harvester's own challenge text names
		// Cloudflare for every wall (net.go FailureMessage).
		lead := "The source is behind an access challenge; the harvester never solves a challenge." + rungs
		if browserRan(result.Rungs) {
			return lead + " The real-browser rung met the wall too, but such walls come and go: a retry later may pass. " +
				"If it does not, " + anotherCopy + "."
		}
		return lead + " Retrying will meet the same wall: " + anotherCopy + "."
	case errorKindLogin:
		if strings.Contains(result.Error, shareSignInText) {
			return result.Error // a share link names its service and the way out (share_links.go); the text carries no URL
		}
		return "The source shows only a sign-in wall; sign-in is required and the harvester never signs in." + rungs +
			" Read a public copy instead: " + anotherCopy + "."
	case errorKindPaywall:
		return "The source is behind a paywall; the harvester never signs in or pays." + rungs + " " + anotherCopyLead + "."
	case errorKindNoOpenCopy:
		return "No open copy of this work could be retrieved: every open-access source and mirror tried failed, " +
			"so the work is likely paywalled, and the harvester never signs in." + rungs +
			" Search for an author preprint with findWorks, or read the publisher's landing page with readPage."
	case errorKindForbidden:
		return fmt.Sprintf("The source refused the harvester (HTTP %d %s): a bot block or an access rule, "+
			"which the harvester cannot tell apart, and it never signs in.%s %s.", status, http.StatusText(status), rungs, anotherCopyLead)
	case errorKindRateLimited:
		return "The source is rate-limiting the harvester (HTTP 429 Too Many Requests)." + rungs + " Retry later, or " + anotherCopy + "."
	case errorKindServer:
		return fmt.Sprintf("The source answered a server error (HTTP %d %s).%s Retry later, or %s.",
			status, http.StatusText(status), rungs, anotherCopy)
	case errorKindMissing:
		return missingMessage(result, rungs)
	case errorKindOversized:
		if strings.HasPrefix(result.Error, unsupportedFormatPrefix) {
			return result.Error // a compressed document past its cap names the cap (resolveFormat)
		}
		return "The document is larger than the harvester's page limit." + rungs + " Save the file with download instead, or choose a smaller copy."
	case errorKindUnsupported:
		if strings.HasPrefix(result.Error, unsupportedFormatPrefix) {
			return result.Error
		}
		return unsupportedFormatText(safeFormatLabel(result.Kind), "")
	case errorKindEmpty:
		if result.Error == localEmptyFileText {
			return result.Error
		}
		return "The source was retrieved but yielded no readable content (empty after extraction)." + rungs + " " + anotherCopyLead + "."
	case errorKindConversion:
		if strings.HasPrefix(result.Error, converterFailureLead) {
			return result.Error // already published: a second pass keeps the converter's words
		}
		if named := converterNamedFailure(result.Error); named != "" {
			return converterFailureLead + named + "." + rungs + " " + anotherCopyLead + "."
		}
		return "The document was retrieved but could not be converted to text (converter or OCR error)." + rungs +
			" Save the file with download, or " + anotherCopy + "."
	case errorKindAppShell:
		return "The page is a JavaScript app shell: no rung, a real browser included, rendered this route's content." + rungs + " " + anotherCopyLead + "."
	case errorKindDisabled:
		return "The provider this read needs is disabled on this harvester." + rungs + " Choose another record with findWorks and read it with readWork, or read a landing page with readPage."
	}
	if strings.HasPrefix(result.Error, unclassifiedLead) {
		return result.Error // already published: a second pass never re-reads its words for a class
	}
	return fmt.Sprintf(
		"%s%s.%s Retry once; if it repeats, %s.",
		unclassifiedLead,
		redactFailureText(result.Error),
		rungs,
		anotherCopy,
	)
}

func missingMessage(result Result, rungs string) string {
	status := result.HTTPStatus
	switch {
	case isLocalFailureSource(result.Source):
		return "The local file does not exist at that path, or cannot be read. Check the path; parseLocalDocuments reads existing files inside the directories this harvester may read."
	case status == http.StatusGone:
		return "The source answered HTTP 410 Gone: the page was removed." + rungs + " " + anotherCopyLead + "."
	case status == http.StatusNotFound:
		return "The source answered HTTP 404 Not Found to every rung that ran: the page was not found, or the site serves 404 to automated clients." + rungs +
			" The harvester cannot tell a missing page from a refusal served as 404." +
			" Check the URL; if it is right, " + anotherCopy + "."
	}
	return "The requested document was not found." + rungs + " Choose a record with findWorks and read it with readWork, or check the URL."
}

// unsupportedFormatPrefix starts every unsupported-format failure the core
// builds itself; the text carries no path, so the public result repeats it.
const unsupportedFormatPrefix = "Unsupported format: a ."

func unsupportedFormatText(format, container string) string {
	detected := ""
	if container != "" {
		detected = " (detected: " + container + ")"
	}
	return unsupportedFormatPrefix + format + " file" + detected + ", which the harvester does not read yet. " +
		"The file exists at its path, unchanged. parseLocalDocuments reads " + ReadableFormats + ": " +
		"convert or extract it to one of those and read that copy."
}

// ReadableFormats is the one list of the document formats the harvester
// converts (converter.py's _CONVERTERS, routed by format_detect.go), named
// in every unsupported-format failure and in parseLocalDocuments' tool
// description.
const ReadableFormats = "PDF, DOC, DOCX, XLS, XLSX, PPTX (with their macro and template variants), " +
	"ODT, ODS, ODP, RTF, EPUB, HTML, CSV, JSON, Markdown and plain text"

var formatLabelPattern = regexp.MustCompile(`^[a-z0-9]{1,12}$`)

func safeFormatLabel(label string) string {
	label = strings.ToLower(strings.TrimPrefix(label, "."))
	if !formatLabelPattern.MatchString(label) {
		return "binary"
	}
	return label
}

// unsupportedLocalFormat reports a local body the harvester does not convert,
// detected by its bytes (resolveFormat): a file (audio, video, image, font,
// executable, archive), a dropped format, or a compressed
// body it cannot open. It names the extension and the detected type.
func unsupportedLocalFormat(path string, body []byte, inflate inflateFunc) (Result, bool) {
	found := resolveFormat(path, body, inflate)
	format := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	if !formatLabelPattern.MatchString(format) {
		format = safeFormatLabel(strings.Fields(found.label + " binary")[0])
	}
	detected := unsupportedFormatPrefix + format + " file (detected: " + found.label + ")"
	var text string
	switch found.class {
	case formatFileOnly:
		text = detected + " is a file, not a document: the harvester does not read it. " +
			"The file exists at its path, unchanged — use it as a file."
	case formatDropped:
		text = detected + ", a format the harvester does not support: it does not read it. " +
			"The file exists at its path, unchanged; convert it to PDF, DOCX or plain text and read that copy."
	case formatRefused:
		text = detected + " " + found.reason + ": the harvester does not read it. " +
			"The file exists at its path, unchanged; decompress it and read the inner document."
	default:
		return Result{}, false
	}
	return Result{Kind: format, ErrorKind: formatErrorKind(found), Error: text}, true
}

// formatRefusalReason is the page sentence for a body readPage does not
// convert because of its type.
func formatRefusalReason(found formatFinding) string {
	if found.class == formatDropped {
		return "its type (" + found.label + ") is a format the harvester does not support."
	}
	return "it " + found.reason + "."
}

func formatErrorKind(found formatFinding) string {
	if found.tooLarge {
		return errorKindTooLarge
	}
	return errorKindUnsupported
}

// browserRanNote is the ladder's sentence for a browser rung that ran and did
// not return the page: a wall only when a challenge was seen, the status
// otherwise — never a 404 described as a wall.
func browserRanNote(challenge bool, status int) string {
	switch {
	case challenge:
		return " The real-browser rung (Patchright + system Chrome) DID run against this wall and still could not pass it."
	case status == http.StatusNotFound || status == http.StatusGone:
		return fmt.Sprintf(" The real-browser rung (Patchright + system Chrome) DID run and got the same HTTP %d: "+
			"the harvester cannot tell a page that is really missing from a refusal the site serves as %d to automated clients.", status, status)
	case status >= 400:
		return fmt.Sprintf(
			" The real-browser rung (Patchright + system Chrome) DID run and got the same HTTP %d.",
			status,
		)
	}
	return " The real-browser rung (Patchright + system Chrome) DID run and still returned no usable page."
}

// browserRefusedNote is the ladder's sentence for a browser rung its SSRF
// guard stopped: a host that does not resolve is not a private address.
func browserRefusedNote(kind string) string {
	if kind == errorKindDNS {
		return " The real-browser rung did not run: the host name does not resolve, so there is no address to load."
	}
	return " The real-browser rung did not run because this server's SSRF guard refused the address (private or internal network). That is policy working as designed, not an outage."
}
