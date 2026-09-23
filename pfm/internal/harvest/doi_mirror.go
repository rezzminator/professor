package harvest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const doiMirrorTimeout = 45 * time.Second

// The forced-OCR rescue runs under its own deadline, not doiMirrorTimeout:
// docling's document_timeout for a scanned PDF outgrows 45 s past three pages.
// doiMirrorOCRMargin covers worker start-up and the reply beyond docling's own
// limit; doiMirrorOCRCap bounds any one rescue and is the deadline of a PDF
// whose page count is not visible (its page tree sits in a compressed object
// stream), so an unknown size never waits without end.
const (
	doiMirrorOCRMargin = 10 * time.Second
	doiMirrorOCRCap    = 15 * time.Minute
)

// pdfPageObject matches one uncompressed page object ("/Type /Page", never the
// "/Type /Pages" tree node).
var pdfPageObject = regexp.MustCompile(`/Type\s*/Page\b`)

// pdfPageCount counts the PDF's uncompressed page objects; 0 means the count
// is not visible in the bytes, never that the document has no page.
func pdfPageCount(body []byte) int {
	return len(pdfPageObject.FindAllIndex(body, -1))
}

// doiMirrorOCRTimeout is the forced-OCR rescue's deadline for a PDF of pages
// pages: docling's own document_timeout — converter.py ocr_timeout, max(30,
// 12 × flagged pages) seconds, a forced run flagging every page — plus
// doiMirrorOCRMargin, capped at doiMirrorOCRCap; pages <= 0 takes the cap.
func doiMirrorOCRTimeout(pages int) time.Duration {
	if pages <= 0 || pages > int(doiMirrorOCRCap/(12*time.Second)) {
		return doiMirrorOCRCap
	}
	docling := time.Duration(max(30, 12*pages)) * time.Second
	return min(docling+doiMirrorOCRMargin, doiMirrorOCRCap)
}

// normalizeDOIMirrorURL validates the configured mirror once at startup. The
// value is copied into settings by New, so a running Harvester never consults
// mutable configuration or process environment while a fetch is in flight.
func normalizeDOIMirrorURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid doi-mirror URL %q: expected an http(s) URL", raw)
	}
	if parsed.Scheme != schemeHTTP && parsed.Scheme != schemeHTTPS {
		return "", fmt.Errorf("invalid doi-mirror URL %q: scheme must be http or https", raw)
	}
	if parsed.User != nil {
		return "", fmt.Errorf("invalid doi-mirror URL %q: URL userinfo is not allowed", raw)
	}
	if parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(raw, "#") {
		return "", fmt.Errorf("invalid doi-mirror URL %q: query and fragment are not allowed", raw)
	}
	if err := validateFetchURL(raw, false); err != nil {
		return "", fmt.Errorf("invalid doi-mirror URL %q: %w", raw, err)
	}
	return raw, nil
}

type doiMirrorLookup struct {
	body      []byte
	pdfURL    string
	pageURL   string
	status    int
	directPDF bool
}

type doiMirrorFailure struct {
	message   string
	kind      string
	challenge bool
	status    int
}

func (f doiMirrorFailure) result(identifier string, rungs []string) Result {
	return Result{
		Source: identifier, Error: "doi-mirror: " + f.message, ErrorKind: f.kind,
		Challenge: f.challenge, HTTPStatus: f.status, Rungs: append([]string(nil), rungs...),
	}
}

// gatewayClient clones base for ONE gateway request: it attaches the request's
// cookie jar and re-validates every redirect hop, without mutating the shared
// client. A nil jar leaves the base client's own jar in place — dropping it
// would silently discard a session an earlier rung established.
func gatewayClient(base *http.Client, jar http.CookieJar) *http.Client {
	clone := &http.Client{Jar: jar}
	var existingRedirect func(*http.Request, []*http.Request) error
	if base != nil {
		baseCopy := *base
		clone = &baseCopy
		if jar != nil {
			clone.Jar = jar
		}
		existingRedirect = clone.CheckRedirect
	}
	clone.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("gateway redirect limit exceeded")
		}
		if err := validateFetchURL(next.URL.String(), false); err != nil {
			return err
		}
		stripCrossHostCredentialHeaders(next, via)
		if existingRedirect != nil {
			return existingRedirect(next, via)
		}
		return nil
	}
	return clone
}

// crossHostRedirectCredentialHeaders lists request headers this package
// sends that carry a provider credential, matching the gatewayRequest.headers
// call sites (oa_sources.go's Semantic Scholar x-api-key, search.go's Brave
// X-Subscription-Token). Go's stdlib already strips Authorization/Cookie-class
// headers on a cross-host redirect (net/http's shouldCopyHeaderOnRedirect);
// it does not know about these, so this policy extends the same protection to
// them (F16).
var crossHostRedirectCredentialHeaders = []string{"X-Api-Key", "X-Subscription-Token"}

// stripCrossHostCredentialHeaders removes this package's own credential
// headers from a redirected request when the hop leaves the ORIGINAL host: a
// same-host redirect (http -> https on the same provider, a trailing slash)
// keeps them, matching stdlib's own same-host behavior for Authorization.
func stripCrossHostCredentialHeaders(next *http.Request, via []*http.Request) {
	if len(via) == 0 || next == nil || next.URL == nil {
		return
	}
	original := via[0].URL
	if original == nil || strings.EqualFold(original.Host, next.URL.Host) {
		return
	}
	for _, name := range crossHostRedirectCredentialHeaders {
		next.Header.Del(name)
	}
}

func doiMirrorMaxBytes(h *Harvester) int64 {
	if h != nil && h.options.MaxBytes > 0 {
		return h.options.MaxBytes
	}
	return 50 * 1024 * 1024
}

// readDOIMirrorResponse is gatewayReadBody's non-truncating branch — every
// gateway caller that refuses an oversize body (postJSON, getJSONWithHeaders,
// searchBrave, the doi-mirror provider itself) reads its response through
// here, so its error strings name the gateway generically rather than the
// one caller ("doi-mirror") this function happened to be written for first.
func readDOIMirrorResponse(
	resp *http.Response,
	maxBytes int64,
) (body []byte, status int, contentType string, returnErr error) {
	if resp == nil {
		return nil, 0, "", errors.New("gateway received no HTTP response")
	}
	status = resp.StatusCode
	contentType = resp.Header.Get("Content-Type")
	if resp.Body == nil {
		return nil, status, contentType, errors.New("gateway received an empty response body")
	}
	decoded, closeBody, err := decodedResponseBody(resp)
	// decodedResponseBody returns a LIVE closer on every return path,
	// including its unsupported-encoding error arm (net.go) — register the
	// close before checking err, or that arm leaks the body and the
	// connection (F5). A named return means `return ..., err` below already
	// sets returnErr, so this defer sees the right value either way.
	defer func() {
		if closeErr := closeBody(); closeErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close response: %w", closeErr))
		}
	}()
	if err != nil {
		return nil, status, contentType, err
	}
	body, err = io.ReadAll(io.LimitReader(decoded, maxBytes+1))
	if err != nil {
		return nil, status, contentType, fmt.Errorf("read response: %w", err)
	}
	if int64(len(body)) > maxBytes {
		return nil, status, contentType, fmt.Errorf("%w (%d bytes)", errResponseTooLarge, maxBytes)
	}
	return body, status, contentType, nil
}

func (h *Harvester) doiMirrorLookup(
	ctx context.Context,
	identifier string,
	jar http.CookieJar,
) (doiMirrorLookup, doiMirrorFailure) {
	base := h.settings.doiMirrorURL
	if base == "" {
		return doiMirrorLookup{}, doiMirrorFailure{message: "provider is disabled", kind: errorKindDisabled}
	}
	if err := validateFetchURL(base, false); err != nil {
		return doiMirrorLookup{}, doiMirrorFailure{message: "lookup URL refused: " + err.Error(), kind: errorKind(err)}
	}
	// The lookup POST goes through the fetch gateway like every other harvester
	// egress, so this provider's own entry point gets the same challenge ladder
	// — Chrome impersonation, then the browser rungs — that the record-page
	// fetches get. It previously ran one plain client, which meant the one
	// request that STARTS a doi-mirror fetch was the one request that could not
	// pass a wall.
	form := url.Values{"request": {identifier}}
	lookup := retrieveRequest{target: base, want: WantPage, policy: PolicyGateway, gateway: gatewayRequest{
		url:    base,
		method: http.MethodPost,
		body:   []byte(form.Encode()),
		client: h.client,
		ua:     h.userAgent,
		headers: http.Header{
			"Content-Type": {mediaTypeForm},
			headerAccept:   {"text/html,application/xhtml+xml,application/pdf;q=0.9,*/*;q=0.8"},
		},
		max:    doiMirrorMaxBytes(h),
		jar:    jar,
		policy: gatewayEscalate,
	}}
	response, err := h.retrieveWith(ctx, lookup)
	body, status := response.Body, response.Status
	pageURL := base
	if response.FinalURL != "" {
		pageURL = response.FinalURL
	}
	if err != nil {
		kind := errorKind(err)
		if strings.Contains(err.Error(), "exceeds") {
			kind = errorKindTooLarge
		}
		// status == 0 means no response ever arrived (a transport failure);
		// a non-zero status means the server answered and the RESPONSE
		// itself then failed to read/decode/fit the ceiling. Collapsing the
		// two into one message hides which side of the wire broke.
		if status == 0 {
			return doiMirrorLookup{}, doiMirrorFailure{
				message: "lookup request failed: " + err.Error(),
				kind:    kind,
				status:  status,
			}
		}
		return doiMirrorLookup{}, doiMirrorFailure{
			message: "lookup response failed: " + err.Error(),
			kind:    kind,
			status:  status,
		}
	}
	if status < 400 && bytes.HasPrefix(body, []byte("%PDF-")) {
		return doiMirrorLookup{body: body, pageURL: pageURL, status: status, directPDF: true}, doiMirrorFailure{}
	}
	if doiMirrorChallenge(body, status) {
		return doiMirrorLookup{}, doiMirrorFailure{
			message:   fmt.Sprintf("lookup returned a CAPTCHA or bot challenge (HTTP %d)", status),
			kind:      errorKindChallenge,
			challenge: true,
			status:    status,
		}
	}
	if status >= 400 {
		return doiMirrorLookup{}, doiMirrorFailure{
			message: fmt.Sprintf("lookup returned HTTP %d", status),
			kind:    schemeHTTP,
			status:  status,
		}
	}
	pdfURL, err := doiMirrorPDFLink(body, pageURL)
	if err != nil {
		return doiMirrorLookup{}, doiMirrorFailure{message: err.Error(), kind: errorKindMissingPDF, status: status}
	}
	return doiMirrorLookup{pdfURL: pdfURL, pageURL: pageURL, status: status}, doiMirrorFailure{}
}

func (h *Harvester) doiMirrorDownload(
	ctx context.Context,
	lookup doiMirrorLookup,
	jar http.CookieJar,
	rungs *[]string,
) ([]byte, int, doiMirrorFailure) {
	if lookup.pdfURL == "" {
		return nil, lookup.status, doiMirrorFailure{
			message: "lookup contained no supported PDF URL",
			kind:    errorKindMissingPDF,
			status:  lookup.status,
		}
	}
	var first *doiMirrorFailure
	for attempt, base := range []*http.Client{h.binaryDirectOrClient(), h.binaryChromeOrChrome()} {
		if attempt == 1 {
			*rungs = append(*rungs, "doi-mirror:chrome")
		}
		if err := validateFetchURL(lookup.pdfURL, false); err != nil {
			failure := doiMirrorFailure{message: "PDF URL refused: " + err.Error(), kind: errorKind(err)}
			if attempt == 0 {
				return nil, 0, failure
			}
			return nil, 0, mergeDOIMirrorFailures(first, failure)
		}
		// Through the gateway like every other egress. Escalation is OFF: this
		// loop owns its own direct→chrome sequence, and the browser rungs can
		// never satisfy a PDF-bytes fetch anyway.
		response, err := gatewayAttempt(ctx, gatewayRequest{
			url:    lookup.pdfURL,
			client: base,
			ua:     h.userAgent,
			headers: http.Header{
				headerAccept:  {"application/pdf,application/octet-stream;q=0.9,*/*;q=0.5"},
				headerReferer: {lookup.pageURL},
			},
			max:    doiMirrorMaxBytes(h),
			jar:    jar,
			binary: true,
		})
		body, status, readErr := response.body, response.status, error(nil)
		if err != nil {
			if status == 0 {
				failure := doiMirrorFailure{message: "PDF request failed: " + err.Error(), kind: errorKind(err)}
				if attempt == 0 {
					first = &failure
					continue
				}
				return nil, 0, mergeDOIMirrorFailures(first, failure)
			}
			// The request reached the server and the RESPONSE failed (decode or
			// size). Keep that distinct from never reaching it.
			readErr = err
		}
		if readErr != nil {
			failure := doiMirrorFailure{
				message: "PDF response failed: " + readErr.Error(),
				kind:    errorKind(readErr),
				status:  status,
			}
			if strings.Contains(readErr.Error(), "exceeds") {
				failure.kind = errorKindTooLarge
				return nil, status, failure
			}
			if attempt == 0 {
				first = &failure
				continue
			}
			return nil, status, mergeDOIMirrorFailures(first, failure)
		}
		if status < 400 && bytes.HasPrefix(body, []byte("%PDF-")) {
			return body, status, doiMirrorFailure{}
		}
		if doiMirrorChallenge(body, status) {
			failure := doiMirrorFailure{
				message:   fmt.Sprintf("PDF request returned a CAPTCHA or bot challenge (HTTP %d)", status),
				kind:      errorKindChallenge,
				challenge: true,
				status:    status,
			}
			if attempt == 0 {
				first = &failure
				continue
			}
			return nil, status, mergeDOIMirrorFailures(first, failure)
		}
		if status >= 400 {
			failure := doiMirrorFailure{
				message: fmt.Sprintf("PDF request returned HTTP %d", status),
				kind:    schemeHTTP,
				status:  status,
			}
			if attempt == 0 {
				first = &failure
				continue
			}
			return nil, status, mergeDOIMirrorFailures(first, failure)
		}
		if !bytes.HasPrefix(body, []byte("%PDF-")) {
			failure := doiMirrorFailure{
				message: "PDF URL returned non-PDF content",
				kind:    errorKindMissingPDF,
				status:  status,
			}
			if first != nil {
				failure = mergeDOIMirrorFailures(first, failure)
			}
			return nil, status, failure
		}
		return body, status, doiMirrorFailure{}
	}
	return nil, 0, doiMirrorFailure{message: "PDF download failed", kind: errorKindConnect}
}

func mergeDOIMirrorFailures(first *doiMirrorFailure, last doiMirrorFailure) doiMirrorFailure {
	if first == nil {
		return last
	}
	last.message = first.message + "; Chrome retry: " + last.message
	last.challenge = last.challenge || first.challenge
	if last.status == 0 {
		last.status = first.status
	}
	return last
}

func (h *Harvester) fetchDOIMirror(ctx context.Context, identifier string, options FetchOptions) Result {
	if h == nil || h.settings.doiMirrorURL == "" {
		return Result{Source: identifier, Error: "doi-mirror provider is disabled", ErrorKind: errorKindDisabled}
	}
	attemptCtx, cancel := context.WithTimeout(ctx, doiMirrorTimeout)
	defer cancel()
	jar, err := cookiejar.New(nil)
	if err != nil {
		return doiMirrorFailure{
			message: "could not create cookie jar: " + err.Error(),
			kind:    errorKindConnect,
		}.result(
			identifier,
			[]string{sourceDOIMirror},
		)
	}
	rungs := []string{sourceDOIMirror}
	lookup, failure := h.doiMirrorLookup(attemptCtx, identifier, jar)
	if failure.message != "" {
		return failure.result(identifier, rungs)
	}
	pdfBody := lookup.body
	pdfStatus := lookup.status
	pdfSource := lookup.pageURL
	if !lookup.directPDF {
		pdfBody, pdfStatus, failure = h.doiMirrorDownload(attemptCtx, lookup, jar, &rungs)
		if failure.message != "" {
			return failure.result(identifier, rungs)
		}
		pdfSource = lookup.pdfURL
	}
	if !bytes.HasPrefix(pdfBody, []byte("%PDF-")) {
		return doiMirrorFailure{
			message: "provider returned non-PDF content",
			kind:    errorKindMissingPDF,
			status:  pdfStatus,
		}.result(
			identifier,
			rungs,
		)
	}
	converted, err := h.convertFetchedContent(attemptCtx, kindPDF, pdfSource, pdfBody)
	if err != nil {
		return doiMirrorFailure{
			message: "PDF conversion failed: " + err.Error(),
			kind:    errorKindConvert,
			status:  pdfStatus,
		}.result(
			identifier,
			rungs,
		)
	}
	if strings.TrimSpace(converted) == "" {
		ocrConverter, ok := h.options.Converter.(OCRConverter)
		if !ok {
			return doiMirrorFailure{
				message: "PDF conversion produced empty text and OCR is unavailable",
				kind:    errorKindConvert,
				status:  pdfStatus,
			}.result(
				identifier,
				rungs,
			)
		}
		rungs = append(rungs, "ocr")
		pages := pdfPageCount(pdfBody)
		ocrTimeout := doiMirrorOCRTimeout(pages)
		ocrCtx, cancelOCR := context.WithTimeout(ctx, ocrTimeout)
		converted, err = ocrConverter.ConvertOCR(ocrCtx, kindPDF, pdfSource, pdfBody)
		ocrDeadline := errors.Is(ocrCtx.Err(), context.DeadlineExceeded)
		cancelOCR()
		if err != nil {
			ocrFailure := doiMirrorFailure{
				message: "PDF conversion produced empty text and OCR failed: " + err.Error(),
				kind:    errorKindConvert,
				status:  pdfStatus,
			}
			if ocrDeadline {
				size := "a PDF whose page count is not visible"
				if pages > 0 {
					size = fmt.Sprintf("%d page(s)", pages)
				}
				ocrFailure.message = fmt.Sprintf(
					"PDF conversion produced empty text and OCR of %s hit its %s deadline: %s",
					size, ocrTimeout, err.Error(),
				)
				ocrFailure.kind = errorKindTimeout
			}
			return ocrFailure.result(
				identifier,
				rungs,
			)
		}
		if !usableContent(converted, kindPDF) {
			return doiMirrorFailure{
				message: "PDF conversion and OCR produced empty text",
				kind:    errorKindConvert,
				status:  pdfStatus,
			}.result(
				identifier,
				rungs,
			)
		}
	}
	stored := h.storeResult(
		pdfSource,
		kindPDF,
		sourceDOIMirror,
		convertedDocument(converted), // the OCR rescue above bypasses convertFetchedContent
		int64(len(pdfBody)),
		pdfStatus,
		rungs,
		options,
	)
	if stored.Error != "" {
		return stored
	}
	stored.Source = identifier
	return stored
}

func doiMirrorChallenge(body []byte, status int) bool {
	if isChallenge(body, status) {
		return true
	}
	low := strings.ToLower(string(body))
	for _, marker := range []string{"altcha-widget", "altcha", "ddos-guard", "ddos guard", challengeMarkerCaptcha, challengeMarkerCloudflare} {
		if strings.Contains(low, marker) {
			return true
		}
	}
	return false
}

func doiMirrorPDFLink(body []byte, finalURL string) (string, error) {
	base, err := url.Parse(finalURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return "", fmt.Errorf("doi-mirror final page URL is invalid")
	}
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("doi-mirror page could not be parsed: %w", err)
	}
	var link string
	var walk func(*html.Node, bool)
	walk = func(node *html.Node, inArticle bool) {
		if link != "" {
			return
		}
		article := inArticle || (node.Type == html.ElementNode && nodeAttr(node, "id") == "article")
		if node.Type == html.ElementNode {
			switch node.Data {
			case "embed", "iframe":
				id := strings.ToLower(nodeAttr(node, "id"))
				if id == kindPDF || (article && nodeAttr(node, "src") != "") {
					link = nodeAttr(node, "src")
				}
			case "object":
				if strings.EqualFold(strings.TrimSpace(nodeAttr(node, "type")), mediaTypePDF) {
					link = nodeAttr(node, "data")
				}
			}
		}
		for child := node.FirstChild; child != nil && link == ""; child = child.NextSibling {
			walk(child, article)
		}
	}
	walk(doc, false)
	if strings.TrimSpace(link) == "" {
		return "", fmt.Errorf("doi-mirror page contained no supported PDF link")
	}
	resolved, err := base.Parse(strings.TrimSpace(link))
	if err != nil || resolved.Scheme == "" || resolved.Host == "" {
		return "", fmt.Errorf("doi-mirror PDF link is invalid")
	}
	resolved.Fragment = ""
	if err := validateFetchURL(resolved.String(), false); err != nil {
		return "", fmt.Errorf("doi-mirror PDF link refused: %w", err)
	}
	return resolved.String(), nil
}

func nodeAttr(node *html.Node, key string) string {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, key) {
			return attr.Val
		}
	}
	return ""
}
