// Package harvest is the pure-Go transport and policy core for Harvester.
//
// It deliberately does not convert documents itself. Conversion is supplied by
// Converter so the existing Python worker can remain the one implementation of
// PDF/Office/HTML extraction while Go owns transport, policy, and caching.
package harvest

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

const (
	schemeHTTP        = "http"
	schemeHTTPS       = "https"
	headerAccept      = "Accept"
	headerReferer     = "Referer"
	headerContentType = "Content-Type"

	extension7Z   = ".7z"
	extensionBMP  = ".bmp"
	extensionGIF  = ".gif"
	extensionJPG  = ".jpg"
	extensionJPEG = ".jpeg"
	extensionMD   = ".md"
	extensionPDF  = ".pdf"
	extensionPNG  = ".png"
	extensionRAR  = ".rar"
	extensionSVG  = ".svg"
	extensionTAR  = ".tar"
	extensionTIF  = ".tif"
	extensionTIFF = ".tiff"
	extensionTXT  = ".txt"
	extensionWebP = ".webp"
	extensionZIP  = ".zip"

	mediaTypeHTML     = "text/html"
	mediaTypePlain    = "text/plain"
	mediaTypePDF      = "application/pdf"
	mediaTypeJSON     = "application/json"
	mediaTypeXHTML    = "application/xhtml+xml"
	mediaTypeXML      = "application/xml"
	mediaTypeTextXML  = "text/xml"
	mediaTypeMarkdown = "text/markdown"

	kindArchive       = "archive"
	kindArchiveMember = "archive_member"
	kind7Z            = "7z"
	kindBMP           = "bmp"
	kindBook          = "book"
	kindCSV           = "csv"
	kindDOCX          = "docx"
	kindEPUB          = "epub"
	kindGIF           = "gif"
	kindHTML          = "html"
	kindImage         = "image"
	kindJPG           = "jpg"
	kindJSON          = "json"
	kindPaper         = "paper"
	kindPDF           = "pdf"
	kindPNG           = "png"
	kindPPTX          = "pptx"
	kindRAR           = "rar"
	kindSVG           = "svg"
	kindTAR           = "tar"
	kindTIFF          = "tiff"
	kindTXT           = "txt"
	kindWebP          = "webp"
	kindXLSX          = "xlsx"
	kindZIP           = "zip"

	errorKindChallenge  = "challenge"
	errorKindBlocked    = "blocked"
	errorKindCancelled  = "cancelled"
	errorKindConnect    = "connect"
	errorKindConversion = "conversion"
	errorKindDisabled   = "disabled"
	errorKindConvert    = "convert"
	errorKindDNS        = "dns"
	errorKindInternal   = "internal"
	errorKindInvalid    = "invalid"
	errorKindMissing    = "missing"
	errorKindMissingPDF = "missing_pdf"
	errorKindOversized  = "oversized"
	errorKindRefused    = "refused"
	errorKindTimeout    = "timeout"
	errorKindTooLarge   = "too_large"
	errorKindWrongKind  = "wrong_kind"

	accessGold   = "gold"
	accessGreen  = "green"
	accessPublic = "public"

	sourceArXiv           = "arxiv"
	sourceCORE            = "core"
	sourceCrossref        = "crossref"
	sourceDOAJ            = "doaj"
	sourceDOIMirror       = "doi-mirror"
	sourceDOIViewer       = "doi-viewer"
	sourceELife           = "elife"
	sourceEuropePMC       = "europepmc"
	sourceGoogleScholar   = "google-scholar"
	sourceGutenberg       = "gutenberg"
	sourceInternetArchive = "internetarchive"
	sourceNBER            = "nber"
	sourceIPFSCatalog     = "ipfs-catalog"
	sourceMD5Catalog      = "md5-catalog"
	sourceOpenAlex        = "openalex"
	sourceOpenAIRE        = "openaire"
	sourceOSF             = "osf"
	sourcePLOS            = "plos"
	sourceSemanticScholar = "semanticscholar"
	sourceUnpaywall       = "unpaywall"
	sourceZenodo          = "zenodo"

	cacheLabel                 = "cache"
	challengeMarkerCaptcha     = "captcha"
	challengeMarkerCloudflare  = "cloudflare"
	cacheStatusHit             = "hit"
	cacheStatusMiss            = "miss"
	cacheStatusRefresh         = "refresh"
	frontmatterSourceHarvester = "harvester"
	localLabel                 = "local"
	localhostName              = "localhost"
	publicDirName              = "public"
	rungChromeImpersonation    = "chrome-impersonation"
	rungDirect                 = "direct"
	searchBackendBrave         = "brave"
	searchBackendSearXNG       = "searxng"
	resultDetailError          = "error"
)

// ProvenanceReferer is the Referer every page-facing request sends — the web
// ladder's direct and chrome rungs, the browser rung's navigation, the image
// loop and the app-shell probe: a visitor arriving from a search result. Some
// anti-bot walls open for a plain GET that carries ANY Referer and refuse a
// Referer-less one.
const ProvenanceReferer = "https://www.google.com/"

// ErrBrowserPolicyDenied marks a browser fetch the SSRF guard refused — a
// private or internal address. It is POLICY, not an outage: the terminal
// message must never tell the caller to retry a permanent refusal, and it
// must never read as proof of IP reputation.
var ErrBrowserPolicyDenied = errors.New("fetch refused by policy (private or internal address)")

// Converter turns one fetched body into markdown. Implementations must not
// mutate body. kind is the detected content kind and source is the original
// source URL/path.
type Converter interface {
	Convert(ctx context.Context, kind, source string, body []byte) (string, error)
}

// OCRConverter is implemented by converters that can force one OCR pass for a
// single document (the dispatch escalation rung for scanned PDFs whose text
// layer converts empty). Optional: a plain Converter simply never escalates.
type OCRConverter interface {
	ConvertOCR(ctx context.Context, kind, source string, body []byte) (string, error)
}

// FullDOMConverter is implemented by converters that can convert an HTML
// page's WHOLE DOM, boilerplate included, instead of extracting its main
// content — the recall gate's fallback when main-content extraction kept too
// little of the page's visible text (recall.go). Optional: without it a
// low-recall conversion is flagged partial instead.
type FullDOMConverter interface {
	ConvertFullDOM(ctx context.Context, source string, body []byte) (string, error)
}

// BrowserFetcher is implemented by adapters that can render one URL in a real
// browser (the ladder's last wall-bypass rung). Optional: a plain Converter
// never escalates to it. headless false asks for a VISIBLE window — the
// ladder spends that only on a wall the headless render could not pass.
type BrowserFetcher interface {
	FetchBrowser(ctx context.Context, source string, headless bool) (html string, status int, err error)
}

// Options configures a Harvester. Nil HTTP clients use safe defaults.
// Every TTL field treats 0 as "use the default" and any NEGATIVE value as an
// explicit zero: CacheTTL < 0 never expires cached documents, NegativeTTL /
// NegativeTransientTTL < 0 never cache failures.
type Options struct {
	Clock          clock.Clock
	CacheDir       string
	CacheTTL       time.Duration
	Client         *http.Client
	Chrome         *http.Client
	Jina           *http.Client
	OA             *http.Client
	Converter      Converter
	MaxBytes       int64
	LocalRoots     []string
	JinaURL        string
	DOIMirrorURL   string
	MaxInlineChars int
	// ProxyURL, when set, is applied to every default transport (direct,
	// Chrome, binary, Jina, and OA). UserAgent customizes only direct/Jina;
	// Chrome keeps its captured impersonation profile and OA keeps its polite
	// scholarly identity.
	ProxyURL  string
	UserAgent string
	// ResolvePublic is called once for each production dial. It is injectable
	// for deterministic DNS-rebinding tests; nil installs the DNS-over-HTTPS
	// resolver (doh.go), because a consumer ISP's resolver can answer a source
	// host with its own block address and no rung can tell that from the real
	// one. The SSRF guard runs on whatever this returns, unchanged.
	ResolvePublic func(context.Context, string) ([]net.IP, error)
	// BrowserRung opts the real-browser rung in (harvester.config.json
	// fetch.browser). The rung is OFF by default: nil or false never starts
	// the browser worker.
	BrowserRung *bool
	// NegativeTTL / NegativeTransientTTL bound the failure caches; zero uses
	// the defaults (120s / 15s).
	NegativeTTL          time.Duration
	NegativeTransientTTL time.Duration
	// Scholarly/search settings come from harvester.config.json through the
	// adapter. The core reads no process environment.
	ContactEmail          string
	GoogleBooksAPIKey     string
	CoreAPIKey            string
	SemanticScholarAPIKey string
	SearXNGURL            string
	BraveAPIKey           string
	IPFSCatalogURL        string
	DOIViewerURL          string
	MD5CatalogURL         string
	GoogleScholarURL      string
	DisableSearch         bool
	// SearchAvailable tells the ladder's own failure messages whether the
	// `search` tool exists to recommend. It is the caller's SearchEnabled(SearchOptions{...})
	// verdict, not re-derived here: the adapter already resolved
	// SearXNGURL/BraveAPIKey/DisableSearch once, and re-deriving it a second
	// way is how a hint drifts from the tool it names.
	SearchAvailable bool
}

// settings is the resolved scholarly/search/browser configuration New takes
// from Options — the one place the ladder reads it from.
type settings struct {
	contactEmail       string
	googleBooksAPIKey  string
	coreAPIKey         string
	semanticScholarKey string
	searXNGURL         string
	braveAPIKey        string
	doiMirrorURL       string
	ipfsCatalogURL     string
	doiViewerURL       string
	md5CatalogURL      string
	googleScholarURL   string
	disableSearch      bool
	searchAvailable    bool
	browser            bool
}

// resolveTTL maps the Options TTL convention onto the cache's: 0 selects the
// default, a negative value is an explicit zero.
func resolveTTL(value, fallback time.Duration) time.Duration {
	switch {
	case value == 0:
		return fallback
	case value < 0:
		return 0
	default:
		return value
	}
}

// Default cache policy, used when an Options field is zero.
const (
	defaultCacheTTL             = 24 * time.Hour
	defaultNegativeTTL          = 120 * time.Second
	defaultNegativeTransientTTL = 15 * time.Second
	defaultMaxInlineChars       = 50000
)

// FetchOptions controls one fetch. Refresh bypasses both positive and
// negative caches; SizeOnly still fetches/caches the complete artifact.
type FetchOptions struct {
	Refresh  bool
	SizeOnly bool
}

// Result is deliberately JSON-friendly so the MCP adapter can return it
// without knowing transport internals.
type Result struct {
	Source       string   `json:"source"`
	Kind         string   `json:"kind,omitempty"`
	Content      string   `json:"content,omitempty"`
	Path         string   `json:"path,omitempty"`
	Method       string   `json:"method,omitempty"`
	CacheStatus  string   `json:"cache_status,omitempty"`
	Bytes        int64    `json:"bytes,omitempty"`
	Chars        int      `json:"chars,omitempty"`
	ContentChars int      `json:"content_chars,omitempty"`
	Tokens       int      `json:"tokens,omitempty"`
	Rungs        []string `json:"rungs,omitempty"`
	Error        string   `json:"error,omitempty"`
	ErrorKind    string   `json:"error_kind,omitempty"`
	Challenge    bool     `json:"challenge,omitempty"`
	HTTPStatus   int      `json:"http_status,omitempty"`
	Members      []Member `json:"members,omitempty"`
	// Partial names why the artifact is known to be INCOMPLETE — a recall gate
	// below its floor, lazy-loaded content still arriving when the browser
	// rung's scroll cap hit, a thread whose comments are only partly in the
	// page. It mirrors the marker line the content itself opens with
	// (partialMarkerPrefix), so every surface that shows the content shows it.
	Partial string `json:"partial,omitempty"`
}

// Harvester owns transport, policy and cache state.
type Harvester struct {
	options      Options
	clock        clock.Clock
	client       *http.Client
	chrome       *http.Client
	binaryDirect *http.Client
	binaryChrome *http.Client
	jina         *http.Client
	oa           *http.Client
	userAgent    string
	cache        *Cache
	neg          *negativeCache
	flightMu     sync.Mutex
	flights      map[string]*fetchFlight
	settings     settings
}

func (h *Harvester) nowClock() clock.Clock {
	if h != nil && h.clock != nil {
		return h.clock
	}
	return clock.Real
}

type fetchFlight struct {
	done   chan struct{}
	result Result
}

// New constructs a Harvester. A nil Converter is valid for callers that only
// need archive listing, search, or raw transport tests; converted fetches then
// report a useful error instead of silently returning bytes. It fails when no
// CacheDir was given and the one default (<home>/.professor/.cache) cannot be
// resolved — never by caching somewhere else.
func New(options Options) (*Harvester, error) {
	if options.Clock == nil {
		options.Clock = clock.Real
	}
	doiMirrorURL, err := normalizeDOIMirrorURL(options.DOIMirrorURL)
	if err != nil {
		return nil, err
	}
	ipfsCatalogURL, err := normalizeProviderBaseURL(sourceIPFSCatalog, options.IPFSCatalogURL)
	if err != nil {
		return nil, err
	}
	doiViewerURL, err := normalizeProviderBaseURL(sourceDOIViewer, options.DOIViewerURL)
	if err != nil {
		return nil, err
	}
	md5CatalogURL, err := normalizeProviderBaseURL(sourceMD5Catalog, options.MD5CatalogURL)
	if err != nil {
		return nil, err
	}
	googleScholarURL, err := normalizeProviderBaseURL("Google Scholar", options.GoogleScholarURL)
	if err != nil {
		return nil, err
	}
	resolved := settings{
		contactEmail:       strings.TrimSpace(options.ContactEmail),
		googleBooksAPIKey:  strings.TrimSpace(options.GoogleBooksAPIKey),
		coreAPIKey:         strings.TrimSpace(options.CoreAPIKey),
		semanticScholarKey: strings.TrimSpace(options.SemanticScholarAPIKey),
		searXNGURL:         strings.TrimSpace(options.SearXNGURL),
		braveAPIKey:        strings.TrimSpace(options.BraveAPIKey),
		doiMirrorURL:       doiMirrorURL,
		ipfsCatalogURL:     ipfsCatalogURL,
		doiViewerURL:       doiViewerURL,
		md5CatalogURL:      md5CatalogURL,
		googleScholarURL:   googleScholarURL,
		disableSearch:      options.DisableSearch,
		searchAvailable:    options.SearchAvailable,
		browser:            options.BrowserRung != nil && *options.BrowserRung,
	}
	options.DOIMirrorURL = doiMirrorURL
	options.IPFSCatalogURL = resolved.ipfsCatalogURL
	options.DOIViewerURL = resolved.doiViewerURL
	options.MD5CatalogURL = resolved.md5CatalogURL
	options.GoogleScholarURL = resolved.googleScholarURL
	if options.CacheDir == "" {
		dir, err := defaultHarvestCacheDir()
		if err != nil {
			return nil, err
		}
		options.CacheDir = dir
	}
	options.CacheTTL = resolveTTL(options.CacheTTL, defaultCacheTTL)
	options.NegativeTTL = resolveTTL(options.NegativeTTL, defaultNegativeTTL)
	options.NegativeTransientTTL = resolveTTL(options.NegativeTransientTTL, defaultNegativeTransientTTL)
	if options.MaxBytes <= 0 {
		options.MaxBytes = 50 * 1024 * 1024
	}
	if options.MaxInlineChars <= 0 {
		options.MaxInlineChars = defaultMaxInlineChars
	}
	if options.JinaURL == "" {
		options.JinaURL = "https://r.jina.ai/"
	}
	// Every client below is built with options.ResolvePublic, and that resolver
	// reaches the dialer through pinnedDialContext/publicIPs — so installing the
	// DoH resolver here is the ONE place that moves all six transports (direct,
	// chrome, both binary tiers, jina, oa) off a resolver the network can
	// rewrite. A caller that supplied its own resolver keeps it.
	if options.ResolvePublic == nil {
		options.ResolvePublic = ResolvePublicHost
	}
	client := options.Client
	customClient := client != nil
	if client == nil {
		client = safeHTTPClient(false, options.ResolvePublic)
	}
	chrome := options.Chrome
	// A legacy adapter may pass one ordinary client in every slot. Never let
	// that silently demote the Chrome rung: equal Client/Chrome pointers mean
	// "unspecified Chrome" and are replaced by the production uTLS client.
	if chrome != nil && chrome == client {
		chrome = nil
	}
	customChrome := chrome != nil
	if chrome == nil {
		chrome = safeHTTPClient(true, options.ResolvePublic)
	}
	binaryDirect := client
	binaryChrome := chrome
	if !customClient {
		binaryDirect = safeHTTPClientTimeout(false, 60*time.Second, options.ResolvePublic)
	}
	if !customChrome {
		binaryChrome = safeHTTPClientTimeout(true, 60*time.Second, options.ResolvePublic)
	}
	jina := options.Jina
	if jina == nil {
		jina = safeHTTPClient(false, options.ResolvePublic)
	}
	oa := options.OA
	if oa == nil {
		if customClient {
			oa = client
		} else {
			// OA metadata calls use the oracle's short 15-second provider
			// timeout; body/document rungs retain their 30/45/60-second tiers.
			oa = safeHTTPClientTimeout(false, 15*time.Second, options.ResolvePublic)
		}
	} else if oa == client && customClient {
		// Keep scholarly requests on their own polite-UA client when an
		// adapter aliases OA to its direct transport.
		oa = safeHTTPClient(false, options.ResolvePublic)
	}
	if options.ProxyURL != "" {
		configureProxy(client, options.ProxyURL)
		configureProxy(chrome, options.ProxyURL)
		configureProxy(binaryDirect, options.ProxyURL)
		configureProxy(binaryChrome, options.ProxyURL)
		configureProxy(jina, options.ProxyURL)
		configureProxy(oa, options.ProxyURL)
	}
	userAgent := options.UserAgent
	if userAgent == "" {
		userAgent = defaultUA
	}
	setUserAgent(client, userAgent)
	setUserAgent(binaryDirect, userAgent)
	setUserAgent(jina, userAgent)
	return &Harvester{
		options:      options,
		clock:        options.Clock,
		client:       client,
		chrome:       chrome,
		binaryDirect: binaryDirect,
		binaryChrome: binaryChrome,
		jina:         jina,
		oa:           oa,
		userAgent:    userAgent,
		cache:        newCache(options.CacheDir, options.CacheTTL, options.Clock),
		neg:          newNegativeCache(options.NegativeTTL, options.NegativeTransientTTL, options.Clock),
		flights:      make(map[string]*fetchFlight),
		settings:     resolved,
	}, nil
}

// Fetch executes one request with default options.
func (h *Harvester) Fetch(ctx context.Context, source string) Result {
	return h.FetchWithOptions(ctx, source, FetchOptions{})
}

// NewChromeClient exposes the production uTLS rung to adapters without
// exposing its transport internals. A nil resolver uses the system resolver.
func NewChromeClient(resolve func(context.Context, string) ([]net.IP, error)) *http.Client {
	return safeHTTPClient(true, resolve)
}

// NewDirectClient is the pinned direct (non-Chrome) client for an adapter
// that needs its own timeout, proxy, or User-Agent but must still dial only
// the addresses the DoH resolver returned. proxy may be nil. An empty ua
// keeps the harvester default; otherwise it is the User-Agent on the wire —
// the transport's own wrapper stamps it on every request, so an adapter's
// outer wrapper cannot lose to it. A nil resolve installs the
// DNS-over-HTTPS resolver (ResolvePublicHost).
func NewDirectClient(
	timeout time.Duration,
	proxy *url.URL,
	ua string,
	resolve func(context.Context, string) ([]net.IP, error),
) *http.Client {
	if resolve == nil {
		resolve = ResolvePublicHost
	}
	client := safeHTTPClientTimeoutWithResolver(false, timeout, resolve)
	setUserAgent(client, ua)
	if proxy != nil {
		if wrapped, ok := client.Transport.(*userAgentTransport); ok {
			if transport, ok := wrapped.base.(*http.Transport); ok {
				transport.Proxy = http.ProxyURL(proxy)
			}
		}
	}
	return client
}

// IsPinnedClient reports whether client's transport is harvest's own pinned
// direct dialer — userAgentTransport wrapping a *http.Transport whose
// DialContext is pinnedDialContext — rather than a bare, unpinned transport.
// It lets an adapter package (harvestmcp) assert its client was built via
// NewDirectClient without reaching into harvest's unexported transport types.
func IsPinnedClient(client *http.Client) bool {
	if client == nil {
		return false
	}
	wrapped, ok := client.Transport.(*userAgentTransport)
	if !ok || wrapped.chrome {
		return false
	}
	transport, ok := wrapped.base.(*http.Transport)
	if !ok {
		return false
	}
	return transport.DialContext != nil
}
