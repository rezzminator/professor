package harvest

import (
	"bytes"
	"context"
	cryptomd5 "crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

const (
	providerTimeout      = 45 * time.Second
	providerHTMLMaxBody  = 4 << 20
	providerCandidateMax = 4
)

var (
	doiViewerViewerFileRe = regexp.MustCompile(`(?i)viewer\.html\?file=([^"'&\s]+)`)
	doiViewerPDFRe        = regexp.MustCompile(`(?i)https?://[^\s"'<>]+\.pdf[^\s"'<>]*`)
	md5CatalogGetRe       = regexp.MustCompile(`(?i)get\.php\?md5=([0-9a-f]{32})&(?:amp;)?key=([A-Za-z0-9]+)`)
	md5Re                 = regexp.MustCompile(`(?i)^[0-9a-f]{32}$`)
	providerMD5Re         = regexp.MustCompile(`(?i)/md5/([0-9a-f]{32})(?:$|[/?#"'\s])`)
	scholarYearRe         = regexp.MustCompile(`\b(?:19|20)\d{2}\b`)
)

type providerLookupError struct {
	message   string
	kind      string
	status    int
	challenge bool
}

func (e *providerLookupError) Error() string { return e.message }

func providerLookupFailure(source, provider string, err error, rungs []string) Result {
	if err == nil {
		return providerResult(source, provider, "lookup failed", errorKindMissing, 0, false, rungs)
	}
	var pe *providerLookupError
	if errors.As(err, &pe) {
		return providerResult(source, provider, pe.message, pe.kind, pe.status, pe.challenge, rungs)
	}
	return providerResult(source, provider, err.Error(), errorKind(err), 0, false, rungs)
}

func providerChallenge(body []byte, status int) bool {
	return doiMirrorChallenge(body, status)
}

type providerCookieJarKey struct{}

func isMirrorProviderMethod(method string) bool {
	switch method {
	case sourceDOIMirror, sourceDOIViewer, sourceMD5Catalog, sourceIPFSCatalog, sourceGoogleScholar:
		return true
	default:
		return false
	}
}

func hasProviderDiagnostic(message string) bool {
	for _, provider := range []string{"doi-mirror:", "doi-viewer:", "md5-catalog:", "ipfs-catalog:", "google-scholar:"} {
		if strings.Contains(message, provider) {
			return true
		}
	}
	return false
}

func normalizeProviderBaseURL(name, raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	base, err := normalizeDOIMirrorURL(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid %s URL: %w", name, err)
	}
	return strings.TrimRight(base, "/"), nil
}

type providerResponse struct {
	body        []byte
	status      int
	contentType string
	finalURL    string
}

// providerGet fetches one provider PAGE through the fetch gateway. Going
// through the gateway is what gives every scholarly provider the challenge
// ladder — Chrome impersonation, then the headless browser, then a headed one
// as the last resort. Before the gateway existed this path ran a single plain
// client, so a provider record page behind a JS wall failed with a bare 403
// that was indistinguishable from the source refusing the request.
func (h *Harvester) providerGet(
	ctx context.Context,
	rawURL string,
	headers http.Header,
) (providerResponse, error) {
	return h.providerFetch(ctx, rawURL, headers, providerHTMLMaxBody, false)
}

// providerDownload fetches a provider ARTIFACT (the PDF/EPUB bytes). It climbs
// the same ladder except the browser rungs, which render HTML and therefore
// can never return a document's bytes.
func (h *Harvester) providerDownload(
	ctx context.Context,
	rawURL string,
	headers http.Header,
	maxBytes int64,
) (providerResponse, error) {
	return h.providerFetch(ctx, rawURL, headers, maxBytes, true)
}

func (h *Harvester) providerFetch(
	ctx context.Context,
	rawURL string,
	headers http.Header,
	maxBytes int64,
	binary bool,
) (providerResponse, error) {
	jar, _ := ctx.Value(providerCookieJarKey{}).(http.CookieJar)
	if jar == nil {
		var err error
		jar, err = cookiejar.New(nil)
		if err != nil {
			return providerResponse{}, fmt.Errorf("create provider cookie jar: %w", err)
		}
	}
	response, err := h.gatewayFetch(ctx, gatewayRequest{
		url:     rawURL,
		client:  h.binaryDirectOrClient(),
		ua:      h.userAgent,
		headers: headers,
		max:     maxBytes,
		jar:     jar,
		policy:  gatewayEscalate,
		binary:  binary,
	})
	if err != nil {
		return providerResponse{status: response.status, contentType: response.contentType}, err
	}
	finalURL := response.finalURL
	if finalURL == "" {
		finalURL = rawURL
	}
	return providerResponse{
		body:        response.body,
		status:      response.status,
		contentType: response.contentType,
		finalURL:    finalURL,
	}, nil
}

func providerResult(source, provider, message, kind string, status int, challenge bool, rungs []string) Result {
	return Result{
		Source:     source,
		Error:      provider + ": " + message,
		ErrorKind:  kind,
		HTTPStatus: status,
		Challenge:  challenge,
		Rungs:      append([]string(nil), rungs...),
	}
}

func providerContext(ctx context.Context, current ...time.Time) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Value(providerCookieJarKey{}).(http.CookieJar); !ok {
		if jar, err := cookiejar.New(nil); err == nil {
			ctx = context.WithValue(ctx, providerCookieJarKey{}, jar)
		} else {
			log.Printf("harvest: provider cookie jar creation failed: %v", err)
		}
	}
	now := clock.Real.Now()
	if len(current) > 0 {
		now = current[0]
	}
	providerDeadline := now.Add(providerTimeout)
	if parentDeadline, ok := ctx.Deadline(); ok && !providerDeadline.Before(parentDeadline) {
		return ctx, func() {}
	}
	return context.WithDeadline(ctx, providerDeadline)
}

func (h *Harvester) fetchProviderArtifact(
	ctx context.Context,
	source, provider, fileURL, referer, expectedMD5 string,
	options FetchOptions,
	rungs []string,
) Result {
	providerCtx := ctx
	var cancel context.CancelFunc
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		providerCtx, cancel = providerContext(ctx, h.nowClock().Now())
		defer cancel()
	}
	return h.fetchProviderArtifactWithPolicy(
		providerCtx,
		source,
		provider,
		fileURL,
		referer,
		expectedMD5,
		options,
		rungs,
		false,
	)
}

func (h *Harvester) fetchProviderArtifactWithPolicy(
	ctx context.Context,
	source, provider, fileURL, referer, expectedMD5 string,
	options FetchOptions,
	rungs []string,
	requirePDF bool,
) Result {
	headers := http.Header{
		headerAccept: {"application/pdf,application/epub+zip,application/octet-stream;q=0.9,*/*;q=0.5"},
	}
	if referer != "" {
		headers.Set(headerReferer, referer)
	}
	response, err := h.providerDownload(ctx, fileURL, headers, doiMirrorMaxBytes(h))
	if err != nil {
		return providerResult(source, provider, "download failed: "+err.Error(), errorKind(err), 0, false, rungs)
	}
	if response.status >= 400 {
		challenge := doiMirrorChallenge(response.body, response.status)
		kind := schemeHTTP
		if challenge {
			kind = errorKindChallenge
		}
		return providerResult(
			source,
			provider,
			fmt.Sprintf("download returned HTTP %d", response.status),
			kind,
			response.status,
			challenge,
			rungs,
		)
	}
	isPDF := bytes.HasPrefix(response.body, []byte("%PDF-"))
	if doiMirrorChallenge(response.body, response.status) && !isPDF {
		return providerResult(
			source,
			provider,
			"download returned a challenge page",
			errorKindChallenge,
			response.status,
			true,
			rungs,
		)
	}
	if requirePDF && !isPDF {
		return providerResult(
			source,
			provider,
			"download did not return PDF bytes",
			errorKindWrongKind,
			response.status,
			false,
			rungs,
		)
	}
	if expectedMD5 != "" {
		digest := cryptomd5.Sum(response.body) // MD5 is the catalog's content key, not a security primitive.
		got := hex.EncodeToString(digest[:])
		if !strings.EqualFold(got, expectedMD5) {
			return providerResult(
				source,
				provider,
				fmt.Sprintf("download failed MD5 verification (got %s, want %s)", got, strings.ToLower(expectedMD5)),
				"integrity",
				response.status,
				false,
				rungs,
			)
		}
	}
	kind := classifyFetchedKind(fileURL, response.contentType, response.body)
	if kind == kindZIP && LooksLikeEpub(response.body) {
		kind = kindEPUB
	}
	if !supportedProviderKind(kind, response.body) {
		return providerResult(
			source,
			provider,
			"download returned an unsupported or non-document format",
			errorKindWrongKind,
			response.status,
			false,
			rungs,
		)
	}
	converted, err := h.convertFetchedContent(ctx, kind, fileURL, response.body)
	if err != nil {
		if kind != kindPDF || !emptyPDFConversionError(err) {
			return providerResult(
				source,
				provider,
				"conversion failed: "+err.Error(),
				errorKindConvert,
				response.status,
				false,
				rungs,
			)
		}
		converted = ""
	}
	if kind == kindPDF && strings.TrimSpace(converted) == "" {
		ocr, ok := h.options.Converter.(OCRConverter)
		if !ok {
			return providerResult(
				source,
				provider,
				"conversion produced empty text and OCR is unavailable",
				errorKindConvert,
				response.status,
				false,
				append(rungs, "ocr"),
			)
		}
		rungs = append(rungs, "ocr")
		converted, err = ocr.ConvertOCR(ctx, kind, fileURL, response.body)
		if err != nil || !usableContent(converted, kind) {
			if err == nil {
				err = errors.New("OCR produced empty text")
			}
			return providerResult(
				source,
				provider,
				"conversion OCR failed: "+err.Error(),
				errorKindConvert,
				response.status,
				false,
				rungs,
			)
		}
	}
	stored := h.storeResult(
		fileURL,
		kind,
		provider,
		converted,
		int64(len(response.body)),
		response.status,
		rungs,
		options,
	)
	if stored.Error != "" {
		stored.Source = source
		return stored
	}
	stored.Source = source
	return stored
}

func emptyPDFConversionError(err error) bool {
	if err == nil {
		return false
	}
	low := strings.ToLower(err.Error())
	return strings.Contains(low, "empty") && (strings.Contains(low, "text") || strings.Contains(low, "markdown"))
}

func supportedProviderKind(kind string, body []byte) bool {
	if kind == kindZIP && LooksLikeEpub(body) {
		return true
	}
	switch kind {
	case kindPDF, kindEPUB, kindDOCX, kindXLSX, kindPPTX, kindCSV, kindTXT, kindHTML:
		return true
	default:
		return false
	}
}

func (r *Resolver) configuredProviderBase(provider string) string {
	if r == nil {
		return ""
	}
	switch provider {
	case sourceIPFSCatalog:
		return strings.TrimRight(strings.TrimSpace(r.IPFSCatalogURL), "/")
	case sourceDOIViewer:
		return strings.TrimRight(strings.TrimSpace(r.DOIViewerURL), "/")
	case sourceMD5Catalog:
		return strings.TrimRight(strings.TrimSpace(r.MD5CatalogURL), "/")
	}
	return ""
}

func (r *Resolver) providerHarvester() *Harvester {
	client := r.client()
	return &Harvester{
		client:       client,
		binaryDirect: client,
		userAgent:    defaultUA,
		options:      Options{MaxBytes: providerHTMLMaxBody},
	}
}

func escapeDOIPath(doi string) string {
	parts := strings.Split(doi, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}
