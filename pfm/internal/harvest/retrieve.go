package harvest

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// Retrieve is the one door every network read of a target goes through. The
// caller names what it wants (a page, converted to Markdown, or a file's
// bytes) and the policy: which rungs it may climb.
//
//   - PolicyPage: the page ladder (fetchURLWithPolicy), unchanged.
//   - PolicyFile: direct → Chrome impersonation → the Wayback raw copy (id_)
//     → browser download (browserFileRung: the browser's download, or the
//     navigation's own response body; headless only). Reader rungs never
//     run: they return text, not bytes.
//   - PolicyInlineImage: direct → Chrome impersonation, with the page as
//     Referer. It never starts a browser: a page with 60 images must not start
//     60 browsers.
//   - PolicyGateway: direct → Chrome → headless browser, for the scholarly
//     providers and the DOI mirror.
//
// File mode streams the body into the binary cache through atomicfile, never
// whole into memory, capped at Options.MaxDownloadBytes (harvest.maxDownloadBytes,
// default 2 GiB). The binary guard of a page read is pageBodyGuard below.
func (h *Harvester) Retrieve(ctx context.Context, target string, want Want, policy Policy) (Retrieved, error) {
	return h.retrieveWith(ctx, retrieveRequest{target: target, want: want, policy: policy})
}

// Want is what a retrieval returns.
type Want int

const (
	// WantPage is Markdown through the converter, with every page check.
	WantPage Want = iota
	// WantFile is the bytes, unparsed.
	WantFile
)

// Policy names the rungs a retrieval may use.
type Policy string

const (
	PolicyPage        Policy = "page"
	PolicyFile        Policy = "file"
	PolicyInlineImage Policy = "inline-image"
	PolicyGateway     Policy = "gateway"
)

const (
	// kindFile names a body that is a file, not a page.
	kindFile                      = "file"
	rungWayback                   = "wayback"
	defaultMaxDownloadBytes int64 = 2 << 30
)

var (
	// errDownloadTooLarge is a file over the download cap: terminal, since the
	// next rung would serve the same file.
	errDownloadTooLarge = errors.New("file exceeds the download cap")
	// errNoFileRung means every rung answered and none served an acceptable
	// file: we looked, and the file was not there — never a transport outage.
	errNoFileRung = errors.New("no rung served the file")
)

// Retrieved is one retrieval's outcome. Result is the page result, or the file
// result (Path in the binary cache). The gateway fields carry the provider
// response a gateway caller parses itself.
type Retrieved struct {
	Result      Result
	Body        []byte
	Status      int
	ContentType string
	FinalURL    string
	Rungs       []string
	Challenge   bool
}

type retrieveRequest struct {
	target  string
	want    Want
	policy  Policy
	options FetchOptions
	referer string
	// accept, when set, is the kinds a file rung may keep; any other body
	// falls to the next rung.
	accept   func(kind string) bool
	maxBytes int64 // file cap; 0 = the harvester's download cap
	// gateway is the full request of a PolicyGateway caller (method, form,
	// headers, cookie jar); empty uses a plain GET of target.
	gateway gatewayRequest
}

func (h *Harvester) retrieveWith(ctx context.Context, req retrieveRequest) (Retrieved, error) {
	switch req.policy {
	case PolicyPage:
		if req.want != WantPage {
			return Retrieved{}, fmt.Errorf(
				"retrieve %s: the page policy reads pages; a file uses the file policy", safeURL(req.target))
		}
		result := h.fetchURLWithPolicy(ctx, req.target, req.options, true)
		return Retrieved{Result: result, Status: result.HTTPStatus, Rungs: result.Rungs}, nil
	case PolicyFile, PolicyInlineImage:
		if req.want != WantFile {
			return Retrieved{}, fmt.Errorf(
				"retrieve %s: the %s policy downloads files; a page uses the page policy",
				safeURL(req.target),
				req.policy,
			)
		}
		return h.retrieveFile(ctx, req)
	case PolicyGateway:
		g := req.gateway
		if g.url == "" {
			g = gatewayRequest{
				url:    req.target,
				client: h.binaryDirectOrClient(),
				ua:     h.userAgent,
				policy: gatewayEscalate,
			}
		}
		g.binary = req.want == WantFile
		response, err := h.retrieveGateway(ctx, g)
		return Retrieved{
			Body:        response.body,
			Status:      response.status,
			ContentType: response.contentType,
			FinalURL:    response.finalURL,
			Rungs:       response.rungs,
			Challenge:   response.challenge,
		}, err
	}
	return Retrieved{}, fmt.Errorf("retrieve %s: unknown policy %q", safeURL(req.target), req.policy)
}

func (h *Harvester) maxDownloadBytes() int64 {
	if h.options.MaxDownloadBytes > 0 {
		return h.options.MaxDownloadBytes
	}
	return defaultMaxDownloadBytes
}

// retrieveFile runs the file rungs; the browser download (browser_download.go)
// is the file policy's last. A browser that ran and ended without the file is
// named in the failure, never an empty success.
func (h *Harvester) retrieveFile(ctx context.Context, req retrieveRequest) (Retrieved, error) {
	var out Retrieved
	if err := validateFetchURL(req.target, false); err != nil {
		return out, err
	}
	limit := req.maxBytes
	if limit <= 0 {
		limit = h.maxDownloadBytes()
	}
	var headers http.Header
	if req.referer != "" {
		headers = http.Header{headerReferer: {req.referer}}
	}
	var lastErr error
	answered := false // a rung got an HTTP answer below 400: the origin is up
	attempt := func(name, target string, client *http.Client, ua string) (bool, error) {
		out.Rungs = append(out.Rungs, name)
		g := gatewayRequest{url: target, client: client, ua: ua, headers: headers}
		result, status, err := h.fileAttempt(ctx, g, req, limit)
		if err != nil {
			lastErr = err
			return false, err
		}
		out.Status = status
		answered = answered || status < 400
		if result.Path == "" {
			return false, nil
		}
		result.Method, result.Rungs = name, append([]string(nil), out.Rungs...)
		out.Result = result
		return true, nil
	}
	for _, rung := range []struct {
		name   string
		client *http.Client
		ua     string
	}{
		{rungDirect, h.binaryDirectOrClient(), h.userAgent},
		{rungChromeImpersonation, h.binaryChromeOrChrome(), chromeUA},
	} {
		if ok, err := attempt(rung.name, req.target, rung.client, rung.ua); ok || errors.Is(err, errDownloadTooLarge) {
			return out, err
		}
	}
	// The Wayback raw copy answers for an origin that is gone or refusing, not
	// for one that served something else: a live answer is the file's truth.
	var waybackErr error
	if req.policy == PolicyFile && !answered && !isPrivateURL(req.target) &&
		!strings.Contains(strings.ToLower(req.target), "web.archive.org") {
		snapshot, err := WaybackRawURL(ctx, h.oa, req.target)
		waybackErr = err
		if err == nil && snapshot != "" {
			if ok, err := attempt(rungWayback, snapshot, h.binaryDirectOrClient(), h.userAgent); ok ||
				errors.Is(err, errDownloadTooLarge) {
				return out, err
			}
		}
	}
	kept, browserErr := h.browserFileRung(ctx, req, limit, &out)
	if kept || errors.Is(browserErr, errDownloadTooLarge) {
		return out, browserErr
	}
	var err error
	switch {
	case lastErr != nil && waybackErr != nil:
		err = fmt.Errorf("%w; wayback lookup failed: %v", lastErr, waybackErr)
	case lastErr != nil:
		err = lastErr
	case waybackErr != nil:
		err = fmt.Errorf("%w (wayback lookup failed: %v)", errNoFileRung, waybackErr)
	default:
		err = errNoFileRung
	}
	if browserErr != nil {
		return out, fmt.Errorf("%w; %v", err, browserErr)
	}
	return out, err
}

// fileAttempt is one streamed file rung: the body goes from the response into
// the binary cache through atomicfile, and only its first 512 bytes are held to
// classify it. An answer it does not keep (>= 400, a kind the caller refuses, a
// wall) returns an empty Result and the status.
func (h *Harvester) fileAttempt(
	ctx context.Context,
	g gatewayRequest,
	req retrieveRequest,
	limit int64,
) (Result, int, error) {
	resp, err := gatewayDo(ctx, g)
	if err != nil {
		return Result{}, 0, err
	}
	decoded, closeBody, err := decodedResponseBody(resp)
	defer func() {
		if closeErr := closeBody(); closeErr != nil {
			obs.Logger(ctx).Warn("harvest: closing a file response body", "url", safeURL(g.url), "error", closeErr)
		}
	}()
	status := resp.StatusCode
	if err != nil {
		return Result{}, status, err
	}
	if status >= 400 {
		return Result{}, status, nil
	}
	if resp.ContentLength > limit {
		return Result{}, status, fmt.Errorf(
			"%w: the server declared %d bytes, over the %d-byte cap (harvest.maxDownloadBytes)",
			errDownloadTooLarge,
			resp.ContentLength,
			limit,
		)
	}
	body := bufio.NewReaderSize(decoded, 512)
	head, err := body.Peek(512)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		return Result{}, status, fmt.Errorf("read response: %w", err)
	}
	kind := classifyKind(req.target, resp.Header.Get("Content-Type"), head)
	if req.accept != nil && !req.accept(kind) {
		return Result{}, status, nil
	}
	if req.accept == nil && (kind == kindHTML || kind == kindTXT) && isChallenge(head, status) {
		return Result{}, status, nil
	}
	path := h.binaryPath(req.target, kind)
	written, err := atomicfile.WriteFrom(path, body, 0o600, limit)
	if errors.Is(err, atomicfile.ErrTooLarge) {
		return Result{}, status, fmt.Errorf(
			"%w: the body ran past the %d-byte cap (harvest.maxDownloadBytes)",
			errDownloadTooLarge,
			limit,
		)
	}
	if err != nil {
		return Result{}, status, fmt.Errorf("cache file: %w", err)
	}
	cache := cacheStatusMiss
	if req.options.Refresh {
		cache = cacheStatusRefresh
	}
	return Result{Source: req.target, Kind: kind, Path: path, CacheStatus: cache, Bytes: written}, status, nil
}

// fileFailure renders a failed file retrieval for what ("image", "archive"):
// every rung answering without the file keeps the plain message and the last
// status; a transport failure or the cap names its cause.
func fileFailure(source, what string, got Retrieved, err error) Result {
	if errors.Is(err, errNoFileRung) {
		return Result{
			Source:     source,
			Error:      what + " could not be downloaded" + strings.TrimPrefix(err.Error(), errNoFileRung.Error()),
			HTTPStatus: got.Status,
			Rungs:      got.Rungs,
		}
	}
	kind := errorKind(err)
	if errors.Is(err, errDownloadTooLarge) {
		kind = errorKindTooLarge
	}
	return Result{
		Source:    source,
		Error:     what + " could not be downloaded: " + err.Error(),
		ErrorKind: kind,
		Rungs:     got.Rungs,
	}
}

// pageBodyGuard is the binary guard of a page read: a body is converted only
// when it is text or a document the converter reads. An image keeps its own
// refusal; any other body the harvester does not convert — a file (audio,
// video, a font, an archive, an unknown binary), a dropped or not-yet-parsed
// format, a decompression bomb — detected by its bytes (resolveFormat), never
// the extension, ends as a named file result, never as page content.
func pageBodyGuard(source, kind string, body []byte, status int, inflate inflateFunc) (Result, bool) {
	if kind == kindImage || isImageKind(kind) {
		return Result{
			Source: source,
			Kind:   kindImage,
			Error: fmt.Sprintf(
				"%s is an image, not a page — download it with `harvester_download_file`; harvester_read reads pages.",
				source,
			),
			HTTPStatus: status,
			ErrorKind:  errorKindWrongKind,
		}, true
	}
	found := resolveFormat(source, body, inflate)
	switch found.class {
	case formatFileOnly:
		if kind == kindZIP || kind == kindTAR || kind == kind7Z || kind == kindRAR {
			return Result{}, false // the ladder names an archive kind itself
		}
		return Result{
			Source: source,
			Kind:   kindFile,
			Error: fmt.Sprintf(
				"%s is a file (%s, %d bytes), not a page — it was not converted; download it with `harvester_download_file`.",
				source,
				found.label,
				len(body),
			),
			HTTPStatus: status,
			ErrorKind:  errorKindWrongKind,
		}, true
	case formatDropped, formatRefused:
		return Result{
			Source: source,
			Kind:   kindFile,
			Error: source + " was not converted: " + formatRefusalReason(found) +
				" Download it with `harvester_download_file`.",
			HTTPStatus: status,
			ErrorKind:  formatErrorKind(found),
		}, true
	}
	return Result{}, false
}
