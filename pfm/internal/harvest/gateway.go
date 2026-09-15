package harvest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
)

// The fetch gateway: the ONE function every harvester HTTP egress goes through.
//
// Before this existed there were two parallel transport paths. The generic web
// ladder (harvest.go) reached direct → chrome → jina → defuddle → browser and
// could therefore pass a JS/bot wall; the scholarly provider path (providerGet)
// reached a single plain client and could not, so a provider whose record page
// sat behind a challenge failed with a bare 403 that read like the source
// refusing us. Same network, same wall, two different outcomes depending on
// which door the call happened to enter.
//
// Every caller now enters here. The gateway owns, in one place:
//
//   - the SSRF/scheme assertion, on the initial URL and on every redirect hop
//   - the User-Agent and per-request headers
//   - the cookie jar (a challenge clearance is worthless if the cookie is lost)
//   - content-encoding decoding and the byte ceiling
//   - the CHALLENGE LADDER below
//
// The challenge ladder, in strict order — cheapest first, most intrusive last:
//
//  1. the caller's client (plain, or the binary tier for downloads)
//  2. Chrome impersonation (uTLS fingerprint; still no JS, no browser surface)
//  3. the real browser, HEADLESS
//  4. the real browser, HEADED — the last resort, and only ever after 3 ran
//
// Rungs 3 and 4 are reached only with gatewayEscalate, only when the rung below
// actually hit a wall, and never for a binary download: the browser rung returns
// rendered HTML, so it cannot produce a PDF's bytes and must not be spent
// pretending it might.
//
// What the gateway does NOT do is solve INTERACTIVE challenges. A wall that
// demands a human click (a "complete the manual check to continue" page) is
// terminal here by design, and is reported as a challenge rather than retried.

// gatewayPolicy selects whether the gateway escalates past the caller's client.
type gatewayPolicy int

const (
	// gatewayNoEscalate runs exactly the client it was handed. Used by callers
	// that own an explicit rung sequence of their own (the generic web
	// ladder's direct and chrome steps), so the ladder's reported rungs stay
	// one-to-one with the requests actually made.
	gatewayNoEscalate gatewayPolicy = iota
	// gatewayEscalate climbs the challenge ladder above until a rung returns
	// content that is not a wall. Used by every scholarly provider call.
	gatewayEscalate
)

// gatewayRequest is one outbound fetch.
type gatewayRequest struct {
	url     string
	client  *http.Client
	ua      string
	headers http.Header
	max     int64
	jar     http.CookieJar
	// policy is read ONLY by gatewayFetch's ladder, to decide whether to
	// escalate past req.client. gatewayAttempt performs exactly one HTTP
	// attempt and never consults this field — a caller that calls
	// gatewayAttempt directly (bypassing the ladder) has nothing to set here.
	policy gatewayPolicy
	// method defaults to GET. body is held as BYTES, not a Reader, because the
	// ladder may replay the same request on a second rung and a consumed
	// stream would replay as an empty one — a POST silently losing its form.
	method string
	body   []byte
	// binary marks a request whose BYTES are the artifact (a PDF, an EPUB).
	// The browser rungs return rendered HTML and can never satisfy one.
	binary bool
	// trustedOrigin marks a URL the OPERATOR configured (a self-hosted SearXNG,
	// which is legitimately allowed to be on loopback). The generic SSRF
	// assertion refuses private hosts, which is right for attacker-supplied
	// URLs and wrong for the operator's own server. Trust extends to the exact
	// configured origin ONLY: the caller's client pins the dial and refuses
	// every redirect, and the gateway must not paper over that refusal with a
	// private-host error that names the wrong cause.
	trustedOrigin bool
	// oversizeTruncate selects what an over-ceiling body means. The generic web
	// ladder KEEPS the permitted prefix (matching the oracle's streaming cap,
	// where a truncated page is still worth converting); the provider path
	// REJECTS it, because half a PDF is not a document. The two semantics are
	// load-bearing in opposite directions, so the gateway carries both rather
	// than picking one and quietly changing a caller.
	oversizeTruncate bool
}

// gatewayResponse is what the gateway returns. rungs lists the rungs actually
// attempted, in order, so a caller's receipt reports the real path taken and
// not an assumed one.
type gatewayResponse struct {
	body        []byte
	status      int
	contentType string
	finalURL    string
	rungs       []string
	challenge   bool
}

// errGatewayNoRung reports that every rung the policy permitted was exhausted
// without a usable answer. It is distinct from a transport error: it means we
// looked and were walled, not that we failed to look.
var errGatewayNoRung = errors.New("every gateway rung was exhausted")

// fetch runs one request through the gateway ladder.
func (h *Harvester) gatewayFetch(ctx context.Context, req gatewayRequest) (gatewayResponse, error) {
	if !req.trustedOrigin {
		if err := assertFetchable(req.url, false); err != nil {
			return gatewayResponse{}, err
		}
	}
	if req.max <= 0 {
		req.max = doiMirrorMaxBytes(h)
	}
	if req.ua == "" {
		req.ua = h.userAgent
	}

	response, err := h.gatewayRung(ctx, req, req.client, "direct", req.ua)
	if req.policy == gatewayNoEscalate {
		return response, err
	}
	if err == nil && !response.challenge {
		return response, nil
	}
	attempted := response.rungs
	// best is the most informative outcome any rung has produced. A rung that
	// RETURNED a response — even a walled one — always outranks a rung that
	// never reached the server, because "this source answered, and its answer
	// was a challenge page" is a fact about the source while "the connection
	// failed" is a fact about our network. Letting a later rung's transport
	// error overwrite an earlier rung's answer would report a wall as a DNS
	// failure and send the caller chasing the wrong problem.
	best, bestErr := response, err

	keep := func(candidate gatewayResponse, candidateErr error) {
		if bestErr != nil && candidateErr == nil {
			best, bestErr = candidate, nil
			return
		}
		if bestErr == nil && candidateErr != nil {
			return // never downgrade a real answer to a transport failure.
		}
		if bestErr != nil && candidateErr != nil {
			// Both rungs failed to reach the server, for possibly different
			// reasons. Keep both: reporting only the first hides the rung that
			// may carry the more diagnostic failure.
			bestErr = errors.Join(bestErr, candidateErr)
			return
		}
		if bestErr == nil && candidateErr == nil && best.challenge && !candidate.challenge {
			best = candidate
		}
	}

	// Rung 2 — Chrome impersonation. Skipped when the caller already handed us
	// the Chrome client: repeating an identical fingerprint against the same
	// wall is a wasted round trip, not a second chance.
	if chrome := h.chromeForGateway(req); chrome != nil {
		attempted = append(attempted, "chrome-impersonation")
		chromeResponse, chromeErr := h.gatewayRung(ctx, req, chrome, "chrome-impersonation", chromeUA)
		if chromeErr == nil && !chromeResponse.challenge {
			chromeResponse.rungs = attempted
			return chromeResponse, nil
		}
		keep(chromeResponse, chromeErr)
	}

	// Rungs 3 and 4 — the real browser. Headless first, always; headed only
	// after headless ran and still met a wall.
	if browserResponse, ok := h.gatewayBrowser(ctx, req, &attempted); ok {
		browserResponse.rungs = attempted
		return browserResponse, nil
	}

	best.rungs = attempted
	if bestErr != nil {
		return best, bestErr
	}
	if best.challenge {
		// A wall we could not pass is an ANSWER about this source from this
		// network, not a transport outage. Return the walled response so the
		// caller renders the challenge diagnostic rather than a connect error.
		return best, nil
	}
	return best, fmt.Errorf("%w for %s", errGatewayNoRung, req.url)
}

// chromeForGateway returns the Chrome-impersonation client to escalate to, or
// nil when escalating would repeat the request we just made.
func (h *Harvester) chromeForGateway(req gatewayRequest) *http.Client {
	chrome := h.chrome
	if req.binary {
		chrome = h.binaryChromeOrChrome()
	}
	if chrome == nil || chrome == req.client {
		return nil
	}
	return chrome
}

// gatewayRung performs exactly one HTTP attempt with the given client, naming
// the rung for the receipt.
func (h *Harvester) gatewayRung(
	ctx context.Context,
	req gatewayRequest,
	client *http.Client,
	name, ua string,
) (gatewayResponse, error) {
	req.client, req.ua = client, ua
	out, err := gatewayAttempt(ctx, req)
	out.rungs = []string{name}
	return out, err
}

// gatewayAttempt is the single HTTP attempt every rung of every caller runs
// through — the one place that owns the SSRF assertion, the User-Agent, the
// per-request headers, the cookie jar, redirect re-validation, content-encoding
// decoding and the byte ceiling. It is a free function so the generic web
// ladder (whose clients are not always the Harvester's own) enters the very
// same code path as the provider ladder.
func gatewayAttempt(ctx context.Context, req gatewayRequest) (gatewayResponse, error) {
	var out gatewayResponse
	if !req.trustedOrigin {
		if err := assertFetchable(req.url, false); err != nil {
			return out, err
		}
	}
	if req.trustedOrigin && req.client == nil {
		// gatewayRequestClient below dereferences *req.client unconditionally
		// for a trusted origin, matching the base client's own redirect
		// policy. A nil client here is a caller bug, not a network failure —
		// name it rather than let the dereference panic the process.
		return out, fmt.Errorf("gateway: trusted-origin request to %s has no client configured", req.url)
	}
	method := req.method
	if method == "" {
		method = http.MethodGet
	}
	var payload io.Reader = http.NoBody
	if len(req.body) > 0 {
		payload = bytes.NewReader(req.body) // a fresh reader per rung; see gatewayRequest.body
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, req.url, payload)
	if err != nil {
		return out, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("User-Agent", req.ua)
	for key, values := range req.headers {
		for _, value := range values {
			httpReq.Header.Add(key, value)
		}
	}
	resp, err := gatewayRequestClient(req).Do(httpReq)
	if err != nil {
		return out, err
	}
	body, status, contentType, err := gatewayReadBody(resp, req.max, req.oversizeTruncate)
	out.status, out.contentType = status, contentType
	if err != nil {
		return out, err
	}
	out.body = body
	out.finalURL = req.url
	if resp.Request != nil && resp.Request.URL != nil {
		out.finalURL = resp.Request.URL.String()
	}
	// A PDF that arrives under a 403 is still the PDF. Only judge a body as a
	// wall when it is not already the artifact.
	if !strings.HasPrefix(string(body), "%PDF-") {
		out.challenge = doiMirrorChallenge(body, status)
	}
	return out, nil
}

// gatewayRequestClient builds the per-request client. A trusted operator origin
// keeps its own client's redirect policy untouched — that policy refuses every
// redirect by name, and wrapping it in the generic SSRF check would report a
// walked-off redirect as a private-host error, naming the wrong cause.
func gatewayRequestClient(req gatewayRequest) *http.Client {
	if req.trustedOrigin {
		clone := *req.client
		if req.jar != nil {
			clone.Jar = req.jar
		}
		return &clone
	}
	return gatewayClient(req.client, req.jar)
}

// gatewayReadBody decodes the response and applies the byte ceiling under the
// caller's oversize rule. truncate keeps the permitted prefix; otherwise an
// over-ceiling body is an error and no partial artifact is returned.
func gatewayReadBody(resp *http.Response, max int64, truncate bool) ([]byte, int, string, error) {
	if !truncate {
		return readDOIMirrorResponse(resp, max)
	}
	if resp == nil {
		return nil, 0, "", errors.New("gateway received no HTTP response")
	}
	status := resp.StatusCode
	contentType := resp.Header.Get("Content-Type")
	if resp.Body == nil {
		return nil, status, contentType, errors.New("gateway received an empty response body")
	}
	decoded, closeBody, err := decodedResponseBody(resp)
	if err != nil {
		return nil, status, contentType, err
	}
	defer func() {
		if closeErr := closeBody(); closeErr != nil {
			log.Printf("harvest: closing gateway response body: %v", closeErr)
		}
	}()
	body, err := io.ReadAll(io.LimitReader(decoded, max+1))
	if err != nil {
		return nil, status, contentType, fmt.Errorf("read response: %w", err)
	}
	if int64(len(body)) > max {
		body = body[:max]
	}
	return body, status, contentType, nil
}

// gatewayBrowser runs the browser rungs: HEADLESS first, and a HEADED window
// only as the final resort after headless met a wall. It reports ok only when a
// render produced content that is not itself a wall.
func (h *Harvester) gatewayBrowser(
	ctx context.Context,
	req gatewayRequest,
	attempted *[]string,
) (gatewayResponse, bool) {
	if req.binary {
		return gatewayResponse{}, false // the rung renders HTML; it has no bytes to give.
	}
	if !h.settings.browser {
		return gatewayResponse{}, false // opt-in (fetch.browser); off means never launched.
	}
	if isPrivateURL(req.url) {
		return gatewayResponse{}, false
	}
	fetcher, ok := h.options.Converter.(BrowserFetcher)
	if !ok {
		log.Printf(
			"harvest: gateway could not escalate %s to the browser rung: no BrowserFetcher adapter is wired into this Harvester",
			req.url,
		)
		return gatewayResponse{}, false
	}

	// renderHeadlessFirst owns the headless-then-headed sequencing; the gateway
	// only records which rungs it spent and judges the result.
	*attempted = append(*attempted, "browser-headless")
	outcome := renderHeadlessFirst(ctx, fetcher, req.url)
	if outcome.headed {
		*attempted = append(*attempted, "browser-headed")
	}
	switch {
	case errors.Is(outcome.err, ErrBrowserPolicyDenied):
		log.Printf("harvest: gateway browser rung refused %s by policy: %v", req.url, outcome.err)
		return gatewayResponse{}, false
	case outcome.err != nil:
		log.Printf("harvest: gateway browser rung could not run for %s: %v", req.url, outcome.err)
		return gatewayResponse{}, false
	}
	if outcome.html != "" && !outcome.wall {
		return browserGatewayResponse(req.url, outcome.html, outcome.status), true
	}
	log.Printf(
		"harvest: gateway browser rungs met a wall for %s (HTTP %d) — this wall is not passable unattended from this network",
		req.url,
		outcome.status,
	)
	return gatewayResponse{}, false
}

// browserRenderOutcome is what the browser rungs produced. It names a WALL
// separately from an OUTAGE and from an empty render, so a caller never has to
// re-derive which of the three it got.
type browserRenderOutcome struct {
	html   string
	status int
	headed bool  // a VISIBLE window was spent
	wall   bool  // the render completed and is still a challenge page
	err    error // the rung could not run at all
}

// renderHeadlessFirst is the ONE implementation of the headless-first /
// headed-last policy, shared by the gateway and the generic web ladder. Two
// copies of this sequencing would drift, and the direction it drifts in is a
// browser window opening on the operator's desktop when it should not.
//
// Headless is ALWAYS attempted. A visible window is spent only on a wall the
// headless render actually met, and a headed launch that fails (a display-less
// host) leaves the completed headless verdict standing rather than masking it.
func renderHeadlessFirst(ctx context.Context, fetcher BrowserFetcher, source string) browserRenderOutcome {
	html, status, err := fetcher.FetchBrowser(ctx, source, true)
	if err != nil {
		return browserRenderOutcome{err: err}
	}
	if html == "" || !isChallenge([]byte(html), status) {
		return browserRenderOutcome{html: html, status: status}
	}
	headedHTML, headedStatus, headedErr := fetcher.FetchBrowser(ctx, source, false)
	if headedErr != nil {
		log.Printf("harvest: headed browser retry for %s could not run after a headless wall: %v", source, headedErr)
		return browserRenderOutcome{html: html, status: status, wall: true}
	}
	return browserRenderOutcome{
		html:   headedHTML,
		status: headedStatus,
		headed: true,
		wall:   headedHTML != "" && isChallenge([]byte(headedHTML), headedStatus),
	}
}

func browserGatewayResponse(source, html string, status int) gatewayResponse {
	return gatewayResponse{
		body:        []byte(html),
		status:      status,
		contentType: "text/html; charset=utf-8",
		finalURL:    source,
	}
}
