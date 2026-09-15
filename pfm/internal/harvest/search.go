package harvest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type SearchOptions struct {
	SearXNGURL    string
	BraveAPIKey   string
	Lang          string
	Engines       string
	Count         int
	SearXNG       *http.Client
	Brave         *http.Client
	DisableSearch bool
}
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
	Engine  string `json:"engine,omitempty"`
}

const searchUA = "harvester-mcp/1.0"

// ErrSearchDisabled and ErrSearchNotConfigured are the two configuration
// failures a caller must tell apart from a backend outage: neither one will
// ever succeed on retry, so an MCP surface renders them verbatim rather than
// folding them into the generic "retry later" backend wording.
var (
	ErrSearchDisabled      = errors.New("search is disabled (search.enabled=false in harvester.config.json)")
	ErrSearchNotConfigured = errors.New(
		"search is not configured: set search.searxngURL and/or search.braveApiKey in harvester.config.json",
	)
)

// SearchBackendError names which configured backend failed. Error() keeps the
// full diagnostic detail (backend name plus the underlying error) for
// internal logging and the joined multi-backend message; SafeMessage is the
// one rendered across the MCP boundary, which may serve a remote client, and
// never names the backend's own URL or repeats its raw error text.
type SearchBackendError struct {
	Backend string // "searxng" or "brave"
	Cause   string // safe, classified cause — see backendCause
	err     error  // full underlying detail, for Error()/Unwrap() only
}

func (e *SearchBackendError) Error() string {
	return fmt.Sprintf("%s: %v", e.Backend, e.err)
}

func (e *SearchBackendError) Unwrap() error { return e.err }

// SafeMessage is the text an MCP surface may render to a remote caller: the
// backend's name and a fixed, classified cause, never its URL or raw error
// text.
func (e *SearchBackendError) SafeMessage() string {
	return fmt.Sprintf("the configured %s backend %s", e.Backend, e.Cause)
}

// backendCause classifies a backend's own failure into the fixed, safe
// causes a caller can act on. Retry is named only for a timeout or a 5xx —
// every other cause is either permanent (bad URL, refused connection) or a
// misconfiguration (SearXNG's json format disabled) that a retry cannot fix.
func backendCause(backend string, status int, err error) string {
	switch {
	case status == 403 && backend == searchBackendSearXNG:
		return "returned HTTP 403 — SearXNG answers 403 when its settings.yml does not enable the json format"
	case status >= 500:
		return fmt.Sprintf("returned HTTP %d — retry", status)
	case status > 0:
		return fmt.Sprintf("returned HTTP %d", status)
	case errorKind(err) == errorKindTimeout:
		return "timed out — retry"
	case errorKind(err) == errorKindConnect:
		return "did not answer (connection refused)"
	case errorKind(err) == errorKindDNS:
		return "could not be resolved (DNS lookup failed)"
	default:
		return "failed"
	}
}

func newBackendError(backend string, status int, err error) *SearchBackendError {
	return &SearchBackendError{Backend: backend, Cause: backendCause(backend, status, err), err: err}
}

// SearchEnabled reports whether a search can run: not disabled, and at least
// one backend configured.
func SearchEnabled(options SearchOptions) bool {
	return !options.DisableSearch && (options.SearXNGURL != "" || options.BraveAPIKey != "")
}

// SearchHint is the one place every harvester message renders its "use
// `search`" recommendation. Naming a disabled tool sends a caller straight
// into a second dead end, so a message that would name it instead falls back
// to withoutSearch, which keeps the rest of the sentence intact and names no
// tool. Every caller — inside this package and across the MCP adapter —
// routes through this helper rather than spelling the clause by hand.
func SearchHint(searchAvailable bool, withSearch, withoutSearch string) string {
	if searchAvailable {
		return withSearch
	}
	return withoutSearch
}

// Search tries configured SearXNG first and Brave second. A configured but
// empty backend returns an empty result with its backend name, while no backend
// is an explicit configuration error. When every configured backend fails the
// error joins each backend's own failure, so the caller sees what broke.
func Search(ctx context.Context, query string, options SearchOptions) ([]SearchResult, string, error) {
	return searchConfigured(ctx, query, options)
}

func searchConfigured(ctx context.Context, query string, options SearchOptions) ([]SearchResult, string, error) {
	if options.DisableSearch {
		return nil, "", ErrSearchDisabled
	}
	if options.SearXNGURL == "" && options.BraveAPIKey == "" {
		return nil, "", ErrSearchNotConfigured
	}
	if options.Count <= 0 {
		options.Count = 8
	}
	if options.Count > 20 {
		options.Count = 20
	}
	var failures []error
	if options.SearXNGURL != "" {
		out, status, e := searchSearXNG(ctx, query, options)
		if e == nil && len(out) > 0 {
			return out, searchBackendSearXNG, nil
		}
		if e == nil && options.BraveAPIKey == "" {
			return out, searchBackendSearXNG, nil
		}
		if e != nil {
			failures = append(failures, newBackendError(searchBackendSearXNG, status, e))
		}
	}
	if options.BraveAPIKey != "" {
		out, status, e := searchBrave(ctx, query, options)
		if e == nil {
			return out, searchBackendBrave, nil
		}
		failures = append(failures, newBackendError(searchBackendBrave, status, e))
	}
	if len(failures) == 1 {
		return nil, resultDetailError, failures[0]
	}
	return nil, resultDetailError, errors.Join(failures...)
}

func (h *Harvester) Search(ctx context.Context, query string, options SearchOptions) ([]SearchResult, string, error) {
	if h != nil {
		if options.SearXNGURL == "" {
			options.SearXNGURL = h.settings.searXNGURL
		}
		if options.BraveAPIKey == "" {
			options.BraveAPIKey = h.settings.braveAPIKey
		}
		options.DisableSearch = options.DisableSearch || h.settings.disableSearch
	}
	return searchConfigured(ctx, query, options)
}

// searxngClient trusts exactly one origin: the operator-configured SearXNG.
// The SearXNG URL is configuration, not content — a loopback or LAN SearXNG is
// its normal deployment — so this client skips the private-address pin that
// guards every fetch. The trust cannot travel: the dialer connects only to the
// configured host:port, no proxy is consulted, and every redirect is refused.
// Fetch, Brave, and every ladder rung keep the full SSRF guard.
func searxngClient(configured string, timeout time.Duration) (*http.Client, error) {
	origin, err := url.Parse(configured)
	if err != nil || origin.Hostname() == "" || (origin.Scheme != schemeHTTP && origin.Scheme != schemeHTTPS) {
		return nil, fmt.Errorf("configured SearXNG URL %q is not an absolute http(s) URL", configured)
	}
	port := origin.Port()
	if port == "" {
		port = "80"
		if origin.Scheme == "https" {
			port = "443"
		}
	}
	allowed := net.JoinHostPort(origin.Hostname(), port)
	dialer := &net.Dialer{Timeout: timeout}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if address != allowed {
				return nil, fmt.Errorf(
					"refusing to dial %s: the search client is pinned to the configured SearXNG origin %s",
					address,
					allowed,
				)
			}
			return dialer.DialContext(ctx, network, address)
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          4,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(next *http.Request, _ []*http.Request) error {
			return fmt.Errorf("refusing redirect from the configured SearXNG to %s", next.URL.Redacted())
		},
	}, nil
}

// searchSearXNG's int return is the HTTP status actually observed, 0 when
// the request never got a response — the caller classifies the failure
// (backendCause) from that status, never from this error's free text, which
// is not safe to surface across the MCP boundary as-is.
func searchSearXNG(ctx context.Context, q string, o SearchOptions) ([]SearchResult, int, error) {
	client := o.SearXNG
	if client == nil {
		trusted, err := searxngClient(o.SearXNGURL, 20*time.Second)
		if err != nil {
			return nil, 0, err
		}
		client = trusted
	}
	u := strings.TrimRight(o.SearXNGURL, "/") + "/search?q=" + url.QueryEscape(q) + "&format=json&safesearch=0"
	if o.Lang != "" {
		u += "&language=" + url.QueryEscape(o.Lang)
	}
	if o.Engines != "" {
		u += "&engines=" + url.QueryEscape(o.Engines)
	}
	// Through the fetch gateway, like every other harvester egress. A search
	// API answers JSON or nothing, so escalation would buy nothing here.
	const maxSearchBody = 10 * 1024 * 1024
	response, e := gatewayAttempt(ctx, gatewayRequest{
		url: u, client: client, ua: searchUA,
		headers:       http.Header{headerAccept: {mediaTypeJSON}},
		max:           maxSearchBody,
		trustedOrigin: true, // the operator's own SearXNG, which may be on loopback
	})
	body, status := response.body, response.status
	if e != nil {
		// status is what the gateway observed before failing (0 when no response
		// arrived) — backendCause classifies from it, so it must survive.
		return nil, status, e
	}
	if status >= 400 {
		return nil, status, fmt.Errorf("HTTP %d", status)
	}
	var data struct {
		Results []struct {
			Title, URL, Content string
			Engines             []string
			Engine              string `json:"engine"`
		} `json:"results"`
	}
	if e = json.Unmarshal(body, &data); e != nil {
		return nil, status, fmt.Errorf("decode SearXNG JSON (is format=json enabled in its settings.yml?): %w", e)
	}
	out := []SearchResult{}
	for _, r := range data.Results {
		if r.URL != "" {
			engine := strings.Join(r.Engines, ",")
			if engine == "" {
				engine = r.Engine
			}
			out = append(
				out,
				SearchResult{Title: r.Title, URL: r.URL, Snippet: truncateRunes(r.Content, 300), Engine: engine},
			)
		}
	}
	if len(out) > o.Count {
		out = out[:o.Count]
	}
	return out, status, nil
}

// searchBrave's int return is the HTTP status actually observed, matching
// searchSearXNG's contract above.
func searchBrave(ctx context.Context, q string, o SearchOptions) ([]SearchResult, int, error) {
	client := o.Brave
	if client == nil {
		client = safeHTTPClientTimeout(false, 20*time.Second)
	}
	query := url.Values{"q": {q}, "count": {fmt.Sprint(o.Count)}}
	if o.Lang != "" {
		query.Set("search_lang", o.Lang)
	}
	reqURL := "https://api.search.brave.com/res/v1/web/search?" + query.Encode()
	// Through the fetch gateway, like every other harvester egress. The
	// subscription token rides a header, so this request must never escalate to
	// a browser rung that would render it somewhere else. This is a
	// JSON-decoding path, so oversizeTruncate is OFF like every other JSON
	// caller: a body over the ceiling is refused with an error naming the
	// ceiling, not silently truncated into a decode failure.
	response, e := gatewayAttempt(ctx, gatewayRequest{
		url:    reqURL,
		client: client,
		ua:     searchUA,
		headers: http.Header{
			headerAccept:           {mediaTypeJSON},
			"X-Subscription-Token": {o.BraveAPIKey},
		},
		max:              10 * 1024 * 1024,
		oversizeTruncate: false,
	})
	body, status := response.body, response.status
	if e != nil {
		return nil, status, e
	}
	if status >= 400 {
		return nil, status, fmt.Errorf("HTTP %d", status)
	}
	var data struct {
		Web struct {
			Results []struct{ Title, URL, Description string } `json:"results"`
		} `json:"web"`
	}
	if e := json.Unmarshal(body, &data); e != nil {
		return nil, status, e
	}
	out := []SearchResult{}
	for _, r := range data.Web.Results {
		if r.URL != "" {
			out = append(
				out,
				SearchResult{
					Title:   r.Title,
					URL:     r.URL,
					Snippet: truncateRunes(r.Description, 300),
					Engine:  searchBackendBrave,
				},
			)
		}
	}
	return out, status, nil
}

func truncateRunes(value string, maxRunes int) string {
	r := []rune(value)
	if len(r) <= maxRunes {
		return value
	}
	return string(r[:maxRunes])
}

// SearchProbeState is the fixed set of health classes doctor may print — a
// caller never asserts one it did not get from ProbeSearch.
type SearchProbeState string

const (
	SearchProbeOff         SearchProbeState = "off"
	SearchProbeReachable   SearchProbeState = "reachable"
	SearchProbeUnreachable SearchProbeState = "unreachable"
	SearchProbeConfigured  SearchProbeState = "configured"
)

// SearchProbe is doctor's one-line verdict on the harvester search tool.
// Warning is set only for a state an operator should act on — OFF and a
// keyless-but-configured Brave key are named states, never warnings.
type SearchProbe struct {
	State   SearchProbeState
	Backend string // "searxng", "brave", or "" when OFF.
	Detail  string
	Warning bool
}

// ProbeSearch reports the search tool's health without spending a real
// query. OFF (disabled or unconfigured) is a named state, not a warning — a
// server that never turned search on is not broken. SearXNG gets a
// GET <url>/healthz: this proves the origin answers HTTP, not that its json
// format (which /search itself needs) is enabled — that distinction is kept
// in the detail text rather than implied by a bare "reachable". Brave is
// never probed: every probe would spend the operator's paid quota, so a
// configured key reports "configured" outright.
func ProbeSearch(ctx context.Context, options SearchOptions, client *http.Client) SearchProbe {
	if options.DisableSearch || (options.SearXNGURL == "" && options.BraveAPIKey == "") {
		return SearchProbe{State: SearchProbeOff, Detail: "OFF (disabled or unconfigured)"}
	}
	if options.SearXNGURL != "" {
		if client == nil {
			client = &http.Client{Timeout: 5 * time.Second}
		}
		// Through the fetch gateway like every other egress; a trusted origin,
		// because the operator's own SearXNG may sit on loopback.
		response, err := gatewayAttempt(ctx, gatewayRequest{
			url: strings.TrimRight(options.SearXNGURL, "/") + "/healthz", client: client, ua: searchUA,
			max:              64 * 1024,
			trustedOrigin:    true,
			oversizeTruncate: true,
		})
		if err != nil {
			return SearchProbe{
				State:   SearchProbeUnreachable,
				Backend: searchBackendSearXNG,
				Detail:  fmt.Sprintf("UNREACHABLE (%s)", backendCause(searchBackendSearXNG, response.status, err)),
				Warning: true,
			}
		}
		if response.status == http.StatusOK {
			return SearchProbe{
				State:   SearchProbeReachable,
				Backend: searchBackendSearXNG,
				Detail:  "reachable (health only — the json format is not probed)",
			}
		}
		return SearchProbe{
			State:   SearchProbeUnreachable,
			Backend: searchBackendSearXNG,
			Detail:  fmt.Sprintf("UNREACHABLE (%s)", backendCause(searchBackendSearXNG, response.status, nil)),
			Warning: true,
		}
	}
	return SearchProbe{
		State:   SearchProbeConfigured,
		Backend: searchBackendBrave,
		Detail:  "configured (key set, not probed — every probe spends quota)",
	}
}
