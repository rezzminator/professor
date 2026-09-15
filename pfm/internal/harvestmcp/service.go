// Package harvestmcp exposes the Harvester's stable six-tool MCP surface.
//
// Transport, cache and policy remain in internal/harvest. This package only
// adapts those operations to MCP and preserves the Python server's receipts.
package harvestmcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	goRuntime "runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hostops/pfm/internal/harvest"
	"hostops/pfm/internal/harvestpy"
)

const (
	defaultInlineChars = 50000
	maxFetchSources    = 50
	maxImageSources    = 50
	maxSearchResults   = 20
	maxFindResults     = 25
	maxCacheResults    = 1000
)

// Runtime is the resolved harvester.config.json plus machine-local paths. The
// MCP service never reads a machine config file or the process environment
// itself; the command passes this already-resolved runtime in.
type Runtime struct {
	Home       string
	CacheDir   string
	LocalRoots []string
	Python     string
	Script     string
	UserAgent  string
	ProxyURL   string
	Client     *http.Client

	SearXNGURL    string
	BraveAPIKey   string
	DisableSearch bool

	ContactEmail          string
	GoogleBooksAPIKey     string
	CoreAPIKey            string
	SemanticScholarAPIKey string
	DOIMirrorURL          string
	IPFSCatalogURL        string
	DOIViewerURL          string
	MD5CatalogURL         string
	GoogleScholarURL      string

	Browser   bool
	PDFOCR    bool
	PDFLayout bool

	// CacheTTL / NegativeTTL / NegativeTransientTTL carry harvester.config.json
	// values when TTLsConfigured is set, where 0 is a real zero (never expire /
	// never cache failures). A zero-value runtime (tests, embeddings) leaves
	// TTLsConfigured false and gets the harvest defaults.
	CacheTTL             time.Duration
	NegativeTTL          time.Duration
	NegativeTransientTTL time.Duration
	TTLsConfigured       bool
	MaxInlineChars       int
}

// Service is one independent Harvester MCP server.
type Service struct {
	server    *mcp.Server
	harvester *harvest.Harvester
	resolver  *harvest.Resolver
	runtime   Runtime
	worker    *harvestpy.Converter
}

// NewConfigured builds the stdio-independent service for tests and command
// wiring. A nil converter is not used: all document conversion is delegated
// to the pinned Python worker selected by the runtime.
func NewConfigured(version string, runtime Runtime) (*Service, error) {
	cacheDir, err := harvest.CacheRoot(runtime.CacheDir)
	if err != nil {
		return nil, fmt.Errorf("resolve harvester cache root: %w", err)
	}
	runtime.CacheDir = cacheDir
	// Keep the resolver's direct client separate from harvest.New's default
	// ladder. Passing one client into every rung silently demotes Chrome and
	// changes the old resolver's polite-UA behavior.
	resolverClient := runtime.Client
	if resolverClient == nil {
		client, err := newHTTPClient(runtime)
		if err != nil {
			return nil, err
		}
		resolverClient = client
	}
	coreRuntime := runtime
	h, worker, err := newHarvester(coreRuntime)
	if err != nil {
		return nil, err
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "harvester", Version: version}, &mcp.ServerOptions{
		Instructions: serverInstructions(searchEnabled(runtime)),
	})
	resolver := &harvest.Resolver{
		Client: resolverClient, ContactEmail: runtime.ContactEmail, GoogleBooksAPIKey: runtime.GoogleBooksAPIKey,
		CoreAPIKey: runtime.CoreAPIKey, SemanticScholarAPIKey: runtime.SemanticScholarAPIKey,
		IPFSCatalogURL:   runtime.IPFSCatalogURL,
		DOIViewerURL:     runtime.DOIViewerURL,
		MD5CatalogURL:    runtime.MD5CatalogURL,
		GoogleScholarURL: runtime.GoogleScholarURL,
	}
	service := &Service{server: server, harvester: h, resolver: resolver, runtime: runtime, worker: worker}
	service.register()
	return service, nil
}

// NewHarvester wires the Go transport to the pinned Python conversion worker.
// The returned Harvester is useful to the CLI, which shares the exact MCP
// fetch semantics without starting a second server.
func NewHarvester(runtime Runtime) (*harvest.Harvester, error) {
	harvester, _, err := newHarvester(runtime)
	return harvester, err
}

func newHarvester(runtime Runtime) (*harvest.Harvester, *harvestpy.Converter, error) {
	// A local CLI/stdin caller owns the machine. Preserve the oracle's
	// unconfined local-read posture (apart from harvest's credential denylist);
	// an empty, non-nil slice prevents the core's deployment-only env roots
	// default from accidentally confining local callers.
	if runtime.LocalRoots == nil {
		runtime.LocalRoots = []string{}
	}
	cacheDir, err := harvest.CacheRoot(runtime.CacheDir)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve harvester cache root: %w", err)
	}
	runtime.CacheDir = cacheDir
	if runtime.Client != nil && runtime.Client.CheckRedirect == nil {
		runtime.Client.CheckRedirect = func(request *http.Request, _ []*http.Request) error {
			parsed := request.URL
			return assertPublicURL(parsed)
		}
	}
	if runtime.Python == "" {
		root, rootErr := harvestStateRoot(runtime)
		if rootErr != nil {
			return nil, nil, rootErr
		}
		current := harvestpy.RuntimeRoot(root, harvestpy.Platform{GOOS: goRuntime.GOOS, GOARCH: goRuntime.GOARCH})
		runtime.Python = filepath.Join(current, "project", ".venv", "bin", "python")
		if runtime.Script == "" {
			runtime.Script = filepath.Join(current, "project", "converter.py")
		}
	}
	worker := harvestpy.NewConverter(
		harvestpy.Runtime{
			Python:    runtime.Python,
			Script:    runtime.Script,
			PDFOCR:    runtime.PDFOCR,
			PDFLayout: runtime.PDFLayout,
		},
	)
	browserRoot, rootErr := harvestStateRoot(runtime)
	if rootErr != nil {
		return nil, nil, rootErr
	}
	converter := pythonConverter{
		worker:      worker,
		browserRoot: browserRoot,
		proxyURL:    runtime.ProxyURL,
	}
	browser := runtime.Browser
	harvester, err := harvest.New(harvest.Options{
		CacheDir:              runtime.CacheDir,
		CacheTTL:              configTTL(runtime.CacheTTL, runtime.TTLsConfigured),
		NegativeTTL:           configTTL(runtime.NegativeTTL, runtime.TTLsConfigured),
		NegativeTransientTTL:  configTTL(runtime.NegativeTransientTTL, runtime.TTLsConfigured),
		MaxInlineChars:        runtime.MaxInlineChars,
		Client:                runtime.Client,
		Chrome:                nil,
		Jina:                  nil,
		OA:                    nil,
		Converter:             converter,
		LocalRoots:            runtime.LocalRoots,
		ProxyURL:              runtime.ProxyURL,
		UserAgent:             runtime.UserAgent,
		BrowserRung:           &browser,
		ContactEmail:          runtime.ContactEmail,
		GoogleBooksAPIKey:     runtime.GoogleBooksAPIKey,
		CoreAPIKey:            runtime.CoreAPIKey,
		SemanticScholarAPIKey: runtime.SemanticScholarAPIKey,
		DOIMirrorURL:          runtime.DOIMirrorURL,
		IPFSCatalogURL:        runtime.IPFSCatalogURL,
		DOIViewerURL:          runtime.DOIViewerURL,
		MD5CatalogURL:         runtime.MD5CatalogURL,
		GoogleScholarURL:      runtime.GoogleScholarURL,
		SearXNGURL:            runtime.SearXNGURL,
		BraveAPIKey:           runtime.BraveAPIKey,
		DisableSearch:         runtime.DisableSearch,
		SearchAvailable:       searchEnabled(runtime),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("construct harvester: %w", err)
	}
	return harvester, worker, nil
}

// configTTL maps a runtime TTL onto harvest.Options, whose 0 means "default"
// and whose negative means "explicit zero": a configured 0 becomes -1, an
// unconfigured runtime passes 0 through to keep the defaults.
func configTTL(value time.Duration, configured bool) time.Duration {
	if configured && value == 0 {
		return -1
	}
	return value
}

// harvestStateRoot resolves the managed harvest-python state root. PFM_HARVEST_ROOT
// is a test-jail override; production lands under the user's state dir.
func harvestStateRoot(runtime Runtime) (string, error) {
	if root := os.Getenv("PFM_HARVEST_ROOT"); root != "" {
		return root, nil
	}
	home := runtime.Home
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve Harvester Python home: %w", err)
		}
	}
	return filepath.Join(home, ".local", "state", "pfm", "harvest-python"), nil
}

func newHTTPClient(runtime Runtime) (*http.Client, error) {
	var proxy *url.URL
	if runtime.ProxyURL != "" {
		parsed, err := url.Parse(runtime.ProxyURL)
		if err != nil {
			return nil, fmt.Errorf("parse Harvester proxy URL: %w", err)
		}
		proxy = parsed
	}
	// harvest.NewDirectClient pins every dial to the DoH-resolved, SSRF-checked
	// address (the same guarantee every other Harvester rung has) instead of
	// the bare http.DefaultTransport clone this resolver client used to dial
	// through. The runtime User-Agent goes INTO the pinned client: harvest's
	// inner wrapper stamps its own UA on every request, so an outer wrapper
	// alone would lose to it on the wire.
	client := harvest.NewDirectClient(60*time.Second, proxy, runtime.UserAgent, harvest.ResolvePublicHost)
	client.Transport = userAgentTransport{base: client.Transport, value: runtime.UserAgent}
	client.CheckRedirect = func(request *http.Request, _ []*http.Request) error {
		return assertPublicURL(request.URL)
	}
	return client, nil
}

type userAgentTransport struct {
	base  http.RoundTripper
	value string
}

func (transport userAgentTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := assertPublicURL(request.URL); err != nil {
		return nil, err
	}
	clone := request.Clone(request.Context())
	if transport.value != "" {
		clone.Header.Set("User-Agent", transport.value)
	}
	return transport.base.RoundTrip(clone)
}

type pythonConverter struct {
	worker *harvestpy.Converter
	// browserRoot is the managed harvest-python state root the opt-in
	// real-browser environment provisions under (env-browser/<platform>).
	browserRoot string
	proxyURL    string
}

// browserHardDeadline is the Go-side ceiling on one browser fetch. The
// 45000ms timeout travels to Python and bounds page.goto only — launch, IPC,
// the headed→headless retry, and close() answer to nobody else. Every other
// transport in this file has a hard Go ceiling; the browser gets one too.
const browserHardDeadline = 3 * time.Minute

// FetchBrowser renders one URL in system Chrome through the opt-in Patchright
// worker — the ladder's last wall-bypass rung. Go owns the SSRF decision:
// every URL Chrome touches is validated here through
// harvest.AssertFetchableStrict — strict, because Chrome re-resolves without a
// pinning hop — and a refusal is re-wrapped as harvest.ErrBrowserPolicyDenied so callers can
// tell POLICY from OUTAGE. Provisioning is lazy and only ever happens after
// fetch.browser gated this method; a missing environment is an outage,
// never a silent skip.
func (converter pythonConverter) FetchBrowser(ctx context.Context, source string, headless bool) (string, int, error) {
	runtime, err := converter.browserRuntime(ctx)
	if err != nil {
		return "", 0, err
	}
	browser := harvestpy.NewBrowserWorker(runtime)
	defer func() { _ = browser.Close() }()

	policyDenied := false
	onAsk := func(url string) error {
		if askErr := harvest.AssertFetchableStrict(url); askErr != nil {
			// Only a refusal of the INITIAL address labels the whole fetch
			// as POLICY. A denied tracker/subresource followed by an
			// unrelated failure must stay an outage — retrying can help.
			if url == source {
				policyDenied = true
			}
			return askErr
		}
		return nil
	}

	fetchCtx, cancel := context.WithTimeout(ctx, browserHardDeadline)
	defer cancel()
	// Pin Chrome to the address DoH resolved and the guard validated. Without
	// this the browser rung resolves the host a second time through the system
	// resolver — so on a network that rewrites DNS answers every HTTP rung
	// would reach the real host while the browser rung alone landed on a block
	// page, and the wall would look like the source's own.
	hostResolverRules := harvest.BrowserHostResolverRule(fetchCtx, source)
	html, status, fetchErr := browser.FetchPinned(
		fetchCtx,
		source,
		converter.proxyURL,
		hostResolverRules,
		headless,
		45000,
		onAsk,
	)
	if fetchErr != nil && policyDenied {
		return "", 0, fmt.Errorf("%w: %v", harvest.ErrBrowserPolicyDenied, fetchErr)
	}
	return html, status, fetchErr
}

// browserRuntime resolves the browser worker's runtime, provisioning it first
// when it is missing or was provisioned by an older pfm (harvestpy.EnsureBrowser).
func (converter pythonConverter) browserRuntime(ctx context.Context) (harvestpy.Runtime, error) {
	return harvestpy.EnsureBrowser(ctx, harvestpy.ProvisionOptions{Root: converter.browserRoot})
}

func (converter pythonConverter) Convert(
	ctx context.Context,
	kind, source string,
	body []byte,
) (markdown string, returnErr error) {
	directory, err := os.MkdirTemp("", "pfm-harvest-input-")
	if err != nil {
		return "", fmt.Errorf("create conversion scratch: %w", err)
	}
	defer func() {
		if cleanupErr := os.RemoveAll(directory); cleanupErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("remove conversion scratch: %w", cleanupErr))
		}
	}()
	extension := strings.TrimPrefix(filepath.Ext(source), ".")
	if extension == "" || extension == source {
		extension = kind
	}
	path := filepath.Join(directory, "input."+extension)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return "", fmt.Errorf("write conversion scratch: %w", err)
	}
	result, convertErr := converter.worker.Convert(ctx, harvestpy.Request{Path: path, Kind: kind, Source: source})
	if convertErr != nil {
		return "", convertErr
	}
	return result.Markdown, nil
}

// ConvertOCR forces ONE Tesseract pass for this document only — the dispatch's
// last-resort rung for scanned PDFs whose text layer converts empty.
func (converter pythonConverter) ConvertOCR(
	ctx context.Context,
	kind, source string,
	body []byte,
) (markdown string, returnErr error) {
	directory, err := os.MkdirTemp("", "pfm-harvest-ocr-")
	if err != nil {
		return "", fmt.Errorf("create OCR scratch: %w", err)
	}
	defer func() {
		if cleanupErr := os.RemoveAll(directory); cleanupErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("remove OCR scratch: %w", cleanupErr))
		}
	}()
	path := filepath.Join(directory, "input."+strings.TrimPrefix(filepath.Ext(source), "."))
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return "", fmt.Errorf("write OCR scratch: %w", err)
	}
	result, convertErr := converter.worker.Convert(
		ctx,
		harvestpy.Request{Path: path, Kind: kind, Source: source, OCR: true},
	)
	if convertErr != nil {
		return "", convertErr
	}
	return result.Markdown, nil
}

// Server exposes the SDK server for in-memory protocol tests.
func (service *Service) Server() *mcp.Server { return service.server }

// Close reaps the managed pinned-Python worker. External and internal HTTP
// gateways share one Service, so callers close only the external Service.
func (service *Service) Close() error {
	if service == nil || service.worker == nil {
		return nil
	}
	return service.worker.Close()
}

// RunStdio serves the stable service over newline-delimited MCP JSON.
func (service *Service) RunStdio(ctx context.Context, input io.Reader, output io.Writer) error {
	return service.server.Run(
		ctx,
		&mcp.IOTransport{Reader: nopReaderCloser{Reader: input}, Writer: nopWriterCloser{Writer: output}},
	)
}

// NewHTTPHandler exposes the same tool surface as stdio through streamable
// HTTP. The process-level daemon supplies authentication and endpoint routing.
func (service *Service) NewHTTPHandler() http.Handler {
	return mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return service.server },
		&mcp.StreamableHTTPOptions{
			JSONResponse:               true,
			Stateless:                  false,
			DisableLocalhostProtection: false,
		},
	)
}

// The SDK's IOTransport takes ownership of ReadCloser/WriteCloser values and
// closes both at connection shutdown. RunStdio accepts the older, deliberately
// non-owning io.Reader/io.Writer contract, so adapt them with no-op closers.
type nopReaderCloser struct{ io.Reader }

func (nopReaderCloser) Close() error { return nil }

type nopWriterCloser struct{ io.Writer }

func (nopWriterCloser) Close() error { return nil }

func (service *Service) register() {
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true}
	mcp.AddTool(
		service.server,
		&mcp.Tool{Name: "fetch", Description: fetchDescription, InputSchema: fetchInputSchema(), Annotations: readOnly},
		func(ctx context.Context, request *mcp.CallToolRequest, input FetchInput) (*mcp.CallToolResult, any, error) {
			result, _, err := service.fetch(ctx, request, input)
			return result, nil, err
		},
	)
	mcp.AddTool(
		service.server,
		&mcp.Tool{
			Name:        "findWorks",
			Description: findDescription,
			InputSchema: findInputSchema(),
			Annotations: readOnly,
		},
		func(ctx context.Context, request *mcp.CallToolRequest, input FindInput) (*mcp.CallToolResult, any, error) {
			result, _, err := service.findWorks(ctx, request, input)
			return result, nil, err
		},
	)
	if searchEnabled(service.runtime) {
		mcp.AddTool(
			service.server,
			&mcp.Tool{
				Name:        "search",
				Description: searchDescription,
				InputSchema: searchInputSchema(),
				Annotations: readOnly,
			},
			func(ctx context.Context, request *mcp.CallToolRequest, input SearchInput) (*mcp.CallToolResult, any, error) {
				result, _, err := service.search(ctx, request, input)
				return result, nil, err
			},
		)
	}
	mcp.AddTool(
		service.server,
		&mcp.Tool{
			Name:        "fetchImage",
			Description: imageDescription,
			InputSchema: imageInputSchema(),
			Annotations: readOnly,
		},
		func(ctx context.Context, request *mcp.CallToolRequest, input ImageInput) (*mcp.CallToolResult, any, error) {
			result, _, err := service.fetchImage(ctx, request, input)
			return result, nil, err
		},
	)
	mcp.AddTool(
		service.server,
		&mcp.Tool{
			Name:        "archive",
			Description: archiveDescription,
			InputSchema: archiveInputSchema(),
			Annotations: readOnly,
		},
		func(ctx context.Context, request *mcp.CallToolRequest, input ArchiveInput) (*mcp.CallToolResult, any, error) {
			result, _, err := service.archive(ctx, request, input)
			return result, nil, err
		},
	)
	mcp.AddTool(
		service.server,
		&mcp.Tool{
			Name:        "searchCache",
			Description: cacheDescription,
			InputSchema: cacheInputSchema(),
			Annotations: readOnly,
		},
		func(ctx context.Context, request *mcp.CallToolRequest, input CacheInput) (*mcp.CallToolResult, any, error) {
			result, _, err := service.searchCache(ctx, request, input)
			return result, nil, err
		},
	)
	service.server.AddPrompt(
		&mcp.Prompt{
			Name:        "fetch",
			Description: "Fetch a URL or local path and convert its contents to markdown",
			Arguments:   []*mcp.PromptArgument{{Name: "url", Description: "URL or path to fetch", Required: true}},
		},
		service.fetchPrompt,
	)
}

const (
	codeTick           = "`"
	fetchDescription   = `Retrieves 1–50 documents as Markdown, input order kept. Call fetch{sources:["https://…","doi:10.…","harvest:…"]} — a URL/path, DOI, ISBN, PMID/PMCID, or a handle from findWorks/searchCache; a title → findWorks first. Each item returns content (may be truncated), size, cache status, and the path to the COMPLETE artifact — read the path for the rest. A failing item carries its own error and the others still return; an empty extraction is reported as that item's error, never as a blank body.`
	findDescription    = `Finds scholarly papers and books by TITLE or bibliographic query — "find the paper about X", "is there a PDF of <title>". No download. Call findWorks{query:"Attention Is All You Need"}. Returns ranked candidates (title, authors, year, kind, access) each with a fetch handle — pass that value unchanged to fetch. Empty candidates = nothing matched (give the exact title); a tool error = discovery itself failed, retry or fetch an exact identifier. For a general web query use search.`
	searchDescription  = `Searches the web — "search for X", "find pages about X" — ranked titles, URLs, and snippets, never the page itself. Call search{query:"…"}; search{query:"…", lang:"ja"} reaches a non-English literature. Pass a result URL to fetch for the document. Empty results = nothing matched (broaden the query); an error result = the search backend failed, retry later. For a paper or book by title use findWorks; for what is already held, searchCache.`
	imageDescription   = `Fetches images as local files for vision — "get this figure / photo / scanned page". Call fetchImage{sources:["https://…/fig1.png"]}, 1–50 URLs, local paths, or harvest handles, results in input order. Returns each item's path and bytes — open the path with vision; images are never OCR'd or converted. A failing item carries its own error and the rest still return. For a document, PDF, or web page use fetch.`
	archiveDescription = `Browses a compressed archive — "open / list / what is in this .zip, .tar.gz, .7z, .rar", then "extract member X from it". Not a web page fetcher — a URL to a page goes to fetch. Call archive{source:"https://…/data.zip"} for the member listing (names and sizes, nothing extracted); then archive{source:"…", member:"docs/readme.md"} converts that one member to Markdown. 0 members = an empty archive; an ERROR item = the archive could not be fetched or opened, or the member was refused (traversal, symlink, size).`
	cacheDescription   = `Greps the local cache of already-fetched documents — "did we already fetch X", "which cached pages mention Y". Call searchCache{pattern:"transformer attention"}. Returns WHICH cached pages match (url, md_path, match count, sample line), not their text — read md_path or fetch the url for content. Empty matches = nothing held matches (the web is not searched; search or fetch first); a tool error = the pattern was invalid or the cache could not be read.`
)

func defaultJSON(value string) json.RawMessage { return json.RawMessage(value) }

func numberSchema(description string, minimum, maximum float64, defaultValue int) *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:        "integer",
		Description: description,
		Minimum:     &minimum,
		Maximum:     &maximum,
		Default:     defaultJSON(strconv.Itoa(defaultValue)),
	}
}

func arraySchema(description string, minimum, maximum int) *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:        "array",
		Description: description,
		Items:       &jsonschema.Schema{Type: "string"},
		MinItems:    &minimum,
		MaxItems:    &maximum,
	}
}

func fetchInputSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "object", Required: []string{"sources"},
		Properties: map[string]*jsonschema.Schema{
			"sources": arraySchema(
				"1–50 things to fetch, each returned as clean Markdown in the SAME order. Each is a LOCATION or an UNAMBIGUOUS identifier of a DOCUMENT: URL / local path / file://; DOI; ISBN; PMID / PMCID; harvest: handle. Use a DIFFERENT tool for a TITLE (findWorks), an IMAGE (fetchImage), or an archive (archive). A failing item returns a descriptive per-item error and the rest still return.",
				1,
				50,
			),
			"refresh": {
				Type:        "boolean",
				Description: "Force a fresh fetch: bypass the cache entirely, re-download, overwrite the cached artifact, and return the NEW content.",
				Default:     defaultJSON("false"),
			},
			"size_only": {
				Type:        "boolean",
				Description: "When true, fetch and cache the full content but return NO body—just {size, chars, path}; full content remains cached at path.",
				Default:     defaultJSON("false"),
			},
		},
	}
}

func findInputSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "object", Required: []string{"query"}, Properties: map[string]*jsonschema.Schema{
		"query": {
			Type:        "string",
			Description: "A paper or book TITLE, or a free-text bibliographic query. Returns a ranked list of candidate works with a fetch handle; pick one and pass it to fetch.",
		},
		"limit": numberSchema("Maximum number of candidate works to return.", 1, 25, 8),
	}}
}

func searchInputSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "object", Required: []string{"query"}, Properties: map[string]*jsonschema.Schema{
		"query": {Type: "string", Description: "The web search query."},
		"count": numberSchema("Maximum number of results to return.", 1, 20, 8),
		"lang": {
			Type:        "string",
			Description: "Optional language/locale to bias the search (e.g. 'zh', 'ja', 'pt-BR'). Set it to reach a NON-English literature.",
		},
		"engines": {
			Type:        "string",
			Description: "Optional comma-separated SearXNG engines to restrict to (e.g. 'google,brave' or 'naver,yahoo'). Omit for the default aggregated set.",
		},
	}}
}

func imageInputSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "object", Required: []string{"sources"}, Properties: map[string]*jsonschema.Schema{
		"sources": arraySchema(
			"1–50 image URLs or local image paths. Each is downloaded into the type-partitioned cache and its LOCAL FILE PATH is returned in order — images are NOT OCR'd.",
			1,
			50,
		),
	}}
}

func archiveInputSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "object", Required: []string{"source"}, Properties: map[string]*jsonschema.Schema{
		"source": {
			Type:        "string",
			Description: "URL or local path of a .zip / .tar(.gz/.bz2/.xz) / .7z / .rar archive.",
		},
		"member": {
			Types:       []string{"string", "null"},
			Description: "Omit to get the SAFE member listing (names + sizes; nothing is extracted). Give one member name from that listing to fetch just that member, converted to Markdown.",
		},
	}}
}

func cacheInputSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "object", Required: []string{"pattern"}, Properties: map[string]*jsonschema.Schema{
		"pattern": {
			Type:        "string",
			Description: "Regex pattern to search across every cached markdown body in the cache.",
		},
		"max_results": numberSchema("Maximum number of matching cached pages to return.", 1, 1000, 50),
		"ignore_case": {Type: "boolean", Description: "Case-insensitive search.", Default: defaultJSON("true")},
	}}
}

type FetchInput struct {
	Sources  []string `json:"sources" jsonschema:"1–50 things to fetch, each returned as clean Markdown in the SAME order. Each is a LOCATION or an UNAMBIGUOUS identifier of a DOCUMENT: URL/local path/file:// or harvest: handle for documents; DOI (bare, doi: prefix, or doi.org URL); ISBN; PMID/PMCID. Use findWorks for a TITLE, fetchImage for an IMAGE, and archive for ZIP/TAR/7z/RAR. A failing item returns a descriptive per-item error and the rest still return."`
	Refresh  bool     `json:"refresh,omitempty" jsonschema:"Force a fresh fetch: bypass the cache entirely, re-download, overwrite the cached artifact, and return the NEW content. Normally cached web pages and documents older than a day are refreshed automatically; images and archives stay cached until refresh."`
	SizeOnly bool     `json:"size_only,omitempty" jsonschema:"When true, fetch and cache the full content but return NO body—just {size, chars, path}, where size is an estimated token count and chars is the raw character count. The full content remains at path."`
}
type FindInput struct {
	Query string `json:"query" jsonschema:"A paper or book TITLE, or a free-text bibliographic query. Returns ranked candidate works with a fetch handle; choose one and pass that handle to fetch."`
	Limit int    `json:"limit,omitempty" jsonschema:"Maximum number of candidate works to return, default 8 and maximum 25."`
}
type ImageInput struct {
	Sources []string `json:"sources" jsonschema:"1–50 image URLs or local image paths. Each is downloaded into the type-partitioned cache and its LOCAL FILE PATH is returned in order for vision. Images are NOT OCR'd."`
}
type ArchiveInput struct {
	Source string `json:"source" jsonschema:"URL or local path of a .zip / .tar(.gz/.bz2/.xz) / .7z / .rar archive."`
	Member string `json:"member,omitempty" jsonschema:"Omit to get the SAFE member listing (names + sizes; nothing extracted). Give one member name from that listing to fetch just that member, converted to Markdown."`
}
type SearchInput struct {
	Query   string `json:"query" jsonschema:"The web search query."`
	Count   int    `json:"count,omitempty" jsonschema:"Maximum number of results to return, default 8 and maximum 20."`
	Lang    string `json:"lang,omitempty" jsonschema:"Optional language/locale to bias the search (e.g. zh, ja, pt-BR). Set it to reach non-English literature."`
	Engines string `json:"engines,omitempty" jsonschema:"Optional comma-separated SearXNG engines to restrict to (e.g. google,brave or naver,yahoo). Omit for the default aggregated set."`
}
type CacheInput struct {
	Pattern    string `json:"pattern" jsonschema:"Regex pattern to search across every cached markdown body in the cache."`
	MaxResults int    `json:"max_results,omitempty" jsonschema:"Maximum number of matching cached pages to return, default 50 and maximum 1000."`
	IgnoreCase *bool  `json:"ignore_case,omitempty" jsonschema:"Case-insensitive search, default true."`
}

type FetchItem struct {
	Source      string   `json:"source"`
	Content     string   `json:"content,omitempty"`
	CacheStatus string   `json:"cache_status,omitempty"`
	Method      string   `json:"-"`
	Bytes       int64    `json:"bytes,omitempty"`
	Tokens      int      `json:"tokens,omitempty"`
	Chars       int      `json:"chars,omitempty"`
	Path        string   `json:"path,omitempty"`
	Error       string   `json:"error,omitempty"`
	Rungs       []string `json:"-"`
	FetchedAt   string   `json:"fetched_at,omitempty"`
}
type FetchOutput struct {
	Items []FetchItem `json:"items"`
}
type FindOutput struct {
	Candidates []harvest.Candidate `json:"candidates"`
}
type SearchOutput struct {
	Results []harvest.SearchResult `json:"results"`
	Backend string                 `json:"-"`
}
type ImageItem struct {
	Source string `json:"source"`
	Path   string `json:"path,omitempty"`
	Bytes  int64  `json:"bytes,omitempty"`
	Error  string `json:"error,omitempty"`
}
type ImageOutput struct {
	Items []ImageItem `json:"items"`
}
type ArchiveOutput struct {
	Result harvest.Result `json:"result"`
}
type CacheHit struct {
	URL     string `json:"url"`
	Path    string `json:"path"`
	Matches int    `json:"matches"`
	Sample  string `json:"sample,omitempty"`
}
type CacheOutput struct {
	Matches []CacheHit `json:"matches"`
}

func (service *Service) fetch(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input FetchInput,
) (*mcp.CallToolResult, FetchOutput, error) {
	if len(input.Sources) < 1 || len(input.Sources) > maxFetchSources {
		return nil, FetchOutput{}, fmt.Errorf("sources must contain 1-%d items", maxFetchSources)
	}
	contents := make([]string, len(input.Sources))
	items := make([]FetchItem, len(input.Sources))
	var wait sync.WaitGroup
	semaphore := make(chan struct{}, 8)
	for index, source := range input.Sources {
		wait.Add(1)
		go func(index int, source string) {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				contents[index] = service.describeFetch(
					source,
					harvest.Result{Source: source, Error: "fetch cancelled: " + ctx.Err().Error()},
					input.SizeOnly,
				)
				items[index] = FetchItem{Source: source, Error: "fetch cancelled: " + ctx.Err().Error()}
				return
			}
			defer func() { <-semaphore }()
			fetched := service.harvester.FetchPublic(
				ctx,
				source,
				harvest.FetchOptions{Refresh: input.Refresh, SizeOnly: input.SizeOnly},
			)
			items[index] = fetchItem(fetched)
			contents[index] = service.describeFetch(source, fetched, input.SizeOnly)
		}(index, source)
	}
	wait.Wait()
	result := &mcp.CallToolResult{}
	for _, text := range contents {
		result.Content = append(result.Content, &mcp.TextContent{Text: text})
	}
	return result, FetchOutput{Items: items}, nil
}

func (service *Service) findWorks(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input FindInput,
) (*mcp.CallToolResult, FindOutput, error) {
	if strings.TrimSpace(input.Query) == "" {
		return nil, FindOutput{}, errors.New("query must not be empty")
	}
	if input.Limit == 0 {
		input.Limit = 8
	}
	if input.Limit < 1 || input.Limit > maxFindResults {
		return nil, FindOutput{}, fmt.Errorf("limit must be between 1 and %d", maxFindResults)
	}
	candidates, err := service.resolver.FindWorks(ctx, input.Query, input.Limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "harvester findWorks: %v\n", err)
		return nil, FindOutput{}, errors.New("Work discovery failed. Retry later or fetch an exact identifier.")
	}
	candidates, err = service.harvester.PublicCandidates(candidates)
	if err != nil {
		fmt.Fprintf(os.Stderr, "harvester export candidates: %v\n", err)
		return nil, FindOutput{}, errors.New("Could not prepare the discovered works for retrieval. Retry later.")
	}
	lines := renderFind(input.Query, candidates)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: lines}},
	}, FindOutput{
		Candidates: candidates,
	}, nil
}

func (service *Service) search(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input SearchInput,
) (*mcp.CallToolResult, SearchOutput, error) {
	if strings.TrimSpace(input.Query) == "" {
		return nil, SearchOutput{}, errors.New("query must not be empty")
	}
	if input.Count == 0 {
		input.Count = 8
	}
	if input.Count < 1 || input.Count > maxSearchResults {
		return nil, SearchOutput{}, fmt.Errorf("count must be between 1 and %d", maxSearchResults)
	}
	results, _, err := harvest.Search(ctx, input.Query, harvest.SearchOptions{
		SearXNGURL:    service.runtime.SearXNGURL,
		BraveAPIKey:   service.runtime.BraveAPIKey,
		DisableSearch: service.runtime.DisableSearch,
		Lang:          input.Lang,
		Engines:       input.Engines,
		Count:         input.Count,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "harvester search: %v\n", err)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: renderSearchFailure(err)}},
			IsError: true,
		}, SearchOutput{}, nil
	}
	for index := range results {
		results[index].Engine = ""
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: renderSearch(input.Query, results, "")}},
	}, SearchOutput{
		Results: results,
	}, nil
}

func (service *Service) fetchImage(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input ImageInput,
) (*mcp.CallToolResult, ImageOutput, error) {
	if len(input.Sources) < 1 || len(input.Sources) > maxImageSources {
		return nil, ImageOutput{}, fmt.Errorf("sources must contain 1-%d items", maxImageSources)
	}
	items := make([]ImageItem, len(input.Sources))
	contents := make([]string, len(input.Sources))
	var wait sync.WaitGroup
	semaphore := make(chan struct{}, 8)
	for index, source := range input.Sources {
		wait.Add(1)
		go func(index int, source string) {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				items[index] = ImageItem{Source: source, Error: "fetch image cancelled: " + ctx.Err().Error()}
				contents[index] = fmt.Sprintf("# %s\nERROR: %s", source, items[index].Error)
				return
			}
			defer func() { <-semaphore }()
			item := service.fetchOneImage(ctx, source)
			items[index] = item
			body, err := json.Marshal(item)
			if err != nil {
				contents[index] = fmt.Sprintf("image receipt encoding failed for %q: %v", source, err)
				return
			}
			contents[index] = string(body)
		}(index, source)
	}
	wait.Wait()
	result := &mcp.CallToolResult{}
	for _, content := range contents {
		result.Content = append(result.Content, &mcp.TextContent{Text: content})
	}
	return result, ImageOutput{Items: items}, nil
}

func (service *Service) archive(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	input ArchiveInput,
) (*mcp.CallToolResult, ArchiveOutput, error) {
	if strings.TrimSpace(input.Source) == "" {
		return nil, ArchiveOutput{}, errors.New("source must not be empty")
	}
	resolved, resolveErr := service.harvester.ResolvePublicSource(input.Source)
	result := harvest.Result{Source: input.Source}
	var err error
	if resolveErr != nil {
		result.Error, result.ErrorKind = resolveErr.Error(), "policy"
		err = resolveErr
	} else {
		result, err = service.harvester.Archive(ctx, resolved, input.Member)
	}
	result = service.harvester.PublicResult(input.Source, result, false)
	if err != nil || result.Error != "" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: service.describeFetch(input.Source, result, false)}},
		}, ArchiveOutput{
			Result: result,
		}, nil
	}
	if input.Member == "" {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: renderArchiveListing(input.Source, result.Members)}},
		}, ArchiveOutput{
			Result: result,
		}, nil
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: service.describeFetch(input.Source, result, false)}},
	}, ArchiveOutput{
		Result: result,
	}, nil
}

func (service *Service) searchCache(
	_ context.Context,
	_ *mcp.CallToolRequest,
	input CacheInput,
) (*mcp.CallToolResult, CacheOutput, error) {
	if strings.TrimSpace(input.Pattern) == "" {
		return nil, CacheOutput{}, errors.New("pattern must not be empty")
	}
	limit := input.MaxResults
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > maxCacheResults {
		return nil, CacheOutput{}, fmt.Errorf("max_results must be between 1 and %d", maxCacheResults)
	}
	ignore := true
	if input.IgnoreCase != nil {
		ignore = *input.IgnoreCase
	}
	cacheHits, err := service.harvester.SearchCachePublic(input.Pattern, limit, ignore)
	if err != nil {
		fmt.Fprintf(os.Stderr, "harvester searchCache: %v\n", err)
		return nil, CacheOutput{}, errors.New("Cache search failed. Check the expression or retry later.")
	}
	hits := make([]CacheHit, 0, len(cacheHits))
	for _, hit := range cacheHits {
		hits = append(hits, CacheHit{URL: hit.URL, Path: hit.Path, Matches: hit.Matches, Sample: hit.Sample})
	}
	lines := make([]string, 0, len(hits)+2)
	if len(hits) == 0 {
		text := fmt.Sprintf(
			"No cached pages match /%s/. This only searches pages already fetched — it does not search the web; %s",
			input.Pattern,
			harvest.SearchHint(searchEnabled(service.runtime),
				"use `search` or `fetch` a source first.",
				"fetch a source first.",
			),
		)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, CacheOutput{
			Matches: hits,
		}, nil
	}
	lines = append(
		lines,
		fmt.Sprintf(
			"%d cached page(s) match /%s/ — this lists WHICH pages match, it does not return their text; `fetch` the source or read `md_path` directly for content:",
			len(hits),
			input.Pattern,
		),
		"",
	)
	for _, hit := range hits {
		lines = append(lines, fmt.Sprintf("- %s  (%d matches)  md_path: %s", hit.URL, hit.Matches, hit.Path))
		if hit.Sample != "" {
			lines = append(lines, "    "+hit.Sample)
		}
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: strings.Join(lines, "\n")}},
	}, CacheOutput{
		Matches: hits,
	}, nil
}

func (service *Service) fetchPrompt(ctx context.Context, request *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	if request == nil || request.Params == nil || request.Params.Arguments == nil ||
		strings.TrimSpace(request.Params.Arguments["url"]) == "" {
		return nil, errors.New("URL is required")
	}
	source := request.Params.Arguments["url"]
	fetched := service.harvester.FetchPublic(ctx, source, harvest.FetchOptions{})
	return &mcp.GetPromptResult{
		Description: fmt.Sprintf("Contents of %s", source),
		Messages: []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{
			Text: service.describeFetch(source, fetched, false),
		}}},
	}, nil
}

func fetchItem(result harvest.Result) FetchItem {
	return FetchItem{
		Source:      result.Source,
		Content:     result.Content,
		CacheStatus: result.CacheStatus,
		Bytes:       result.Bytes,
		Tokens:      result.Tokens,
		Chars:       result.Chars,
		Path:        result.Path,
		Error:       result.Error,
	}
}

func (service *Service) describeFetch(source string, result harvest.Result, sizeOnly bool) string {
	if result.Error != "" {
		return "# " + source + "\nERROR: " + harvest.PublicFailureMessage(result)
	}
	// Size probes never kill an empty extraction behind a zero-sized success;
	// this wording is part of the Python scheduler contract.
	if sizeOnly {
		if strings.TrimSpace(result.Content) == "" && result.Chars == 0 && result.Bytes == 0 {
			return fmt.Sprintf(
				"# %s\nERROR: Fetched %s but it yielded no readable content (empty after extraction) — nothing to size. %s",
				source,
				source,
				harvest.SearchHint(searchEnabled(service.runtime),
					"Use `search` to find an alternative copy, or `findWorks` if it is a scholarly title.",
					"Use `findWorks` if it is a scholarly title, or fetch an alternative copy at another URL.",
				),
			)
		}
		body, err := json.Marshal(
			map[string]any{
				"source":       source,
				"size":         result.Tokens,
				"tokens":       result.Tokens,
				"token_count":  result.Tokens,
				"chars":        result.Chars,
				"path":         result.Path,
				"cache_status": result.CacheStatus,
			},
		)
		if err != nil {
			return fmt.Sprintf("# %s\nERROR: encode size receipt: %v", source, err)
		}
		return string(body)
	}
	body := result.Content
	stripped := strings.TrimSpace(body)
	meta := frontmatter(result.Path)
	status := result.HTTPStatus
	if stripped == "" {
		message := ""
		if result.ErrorKind != "" || result.Challenge || status >= 400 {
			message = harvest.PublicFailureMessage(result)
		}
		if message == "" {
			message = fmt.Sprintf(
				"Fetched %s but no readable content could be extracted (JS-rendered or bot-blocked — not retrievable from this datacenter IP). %s",
				source,
				harvest.SearchHint(searchEnabled(service.runtime),
					"Use `search` to find an alternative copy, or `findWorks` if it is a scholarly title.",
					"Use `findWorks` if it is a scholarly title, or fetch an alternative copy at another URL.",
				),
			)
		}
		return "# " + source + "\nERROR: " + message
	}
	// A thin HTTP error/challenge page is a failure, not a successful body.
	lowerBody := strings.ToLower(stripped)
	challenge := strings.Contains(lowerBody, "cloudflare") || strings.Contains(lowerBody, "captcha") ||
		strings.Contains(lowerBody, "verify you are human")
	if len([]rune(stripped)) < 500 && (status >= 400 || challenge) {
		return "# " + source + "\nERROR: " + harvest.PublicFailureMessage(
			harvest.Result{HTTPStatus: status, ErrorKind: result.ErrorKind, Challenge: challenge},
		)
	}
	fetchedAt := "unknown"
	if value := meta["fetched_at"]; value != "" {
		fetchedAt = value
	}
	tokens := result.Tokens
	if value, err := strconv.Atoi(meta["token_count"]); err == nil {
		tokens = value
	}
	header := fmt.Sprintf(
		"# %s\ncache_status: %s / bytes: %d / tokens: %d / fetched_at: %s / path: %s",
		source,
		result.CacheStatus,
		result.Bytes,
		tokens,
		fetchedAt,
		result.Path,
	)

	cap := service.inlineCap()
	if cap > 0 && len([]rune(body)) > cap {
		runes := []rune(body)
		body = string(
			runes[:cap],
		) + fmt.Sprintf(
			"\n\n— [truncated: first %d of %d chars. COMPLETE text is at %s — read that file from char %d for the rest. `searchCache` locates WHICH cached pages match a pattern; it does not return text.]",
			cap,
			max(result.Chars, len(runes)),
			result.Path,
			cap,
		)
	}
	return header + "\n\n" + body
}

// inlineCap is output.maxInlineChars from harvester.config.json; an
// unconfigured runtime keeps the default.
func (service *Service) inlineCap() int {
	if service != nil && service.runtime.MaxInlineChars > 0 {
		return service.runtime.MaxInlineChars
	}
	return defaultInlineChars
}

func frontmatter(path string) map[string]string {
	out := map[string]string{}
	if path == "" {
		return out
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	text := string(data)
	if !strings.HasPrefix(text, "---\n") {
		return out
	}
	end := strings.Index(text[4:], "\n---\n")
	if end < 0 {
		return out
	}
	for _, line := range strings.Split(text[4:4+end], "\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok {
			out[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return out
}

func renderFind(query string, candidates []harvest.Candidate) string {
	if len(candidates) == 0 {
		return fmt.Sprintf(
			"No candidate works found for %q. Try a plain WebSearch, or rephrase — a more exact title helps.",
			query,
		)
	}
	lines := []string{
		fmt.Sprintf(
			"%d candidate work(s) for %q — pick one and call `fetch` with its `fetch:` value:",
			len(candidates),
			query,
		),
		"",
	}
	for index, candidate := range candidates {
		values := []string{candidate.Kind}
		if candidate.Authors != "" {
			values = append(values, candidate.Authors)
		}
		if candidate.Year != 0 {
			values = append(values, strconv.Itoa(candidate.Year))
		}
		if candidate.Free != "" {
			values = append(values, candidate.Free)
		}
		values = append(values, fmt.Sprintf("match %v", candidate.Match))
		meta := strings.Trim(strings.Join(values, " · "), " ·")
		lines = append(
			lines,
			fmt.Sprintf("%d. %s", index+1, valueOr(candidate.Title, "(untitled)")),
			"   fetch: "+candidate.URL,
			"   "+meta,
		)
	}
	return strings.Join(lines, "\n")
}

func renderArchiveListing(source string, members []harvest.Member) string {
	lines := []string{
		"# Archive: " + source,
		"",
		fmt.Sprintf(
			"%d member(s). Fetch one with `archive(source=%q, member=\"<name>\")` — pick a name from the table below.",
			len(members),
			source,
		),
		"",
		"| name | size (bytes) | type |",
		"| --- | --- | --- |",
	}
	for _, member := range members {
		typeName := "file"
		if member.IsDir {
			typeName = "dir"
		} else if member.IsSymlink {
			typeName = "symlink"
		}
		name := strings.ReplaceAll(member.Name, "|", "\\|")
		lines = append(lines, fmt.Sprintf("| %s | %d | %s |", name, member.UncompressedSize, typeName))
	}
	return strings.Join(lines, "\n") + "\n"
}

func renderSearch(query string, results []harvest.SearchResult, _ string) string {
	if len(results) == 0 {
		return fmt.Sprintf("No results for %q. Try different terms or a broader query.", query)
	}
	lines := []string{fmt.Sprintf("%d result(s) for %q — fetch the ones you want by URL:", len(results), query), ""}
	for index, result := range results {
		lines = append(lines, fmt.Sprintf("%d. %s", index+1, valueOr(result.Title, "(untitled)")), "   "+result.URL)
		if result.Snippet != "" {
			lines = append(lines, "   "+result.Snippet)
		}
	}
	return strings.Join(lines, "\n")
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func (service *Service) fetchOneImage(ctx context.Context, source string) ImageItem {
	resolved, err := service.harvester.ResolvePublicSource(source)
	result := harvest.Result{Source: source}
	if err != nil {
		result.Error, result.ErrorKind = err.Error(), "policy"
	} else {
		result = service.harvester.FetchImage(ctx, resolved)
	}
	result = service.harvester.PublicResult(source, result, true)
	return ImageItem{Source: source, Path: result.Path, Bytes: result.Bytes, Error: result.Error}
}

// assertPublicURL delegates to harvest's own SSRF/scheme chokepoint
// (AssertFetchable) instead of keeping a second, drifting copy of the
// private/internal check here — harvest.assertFetchable is the one
// authority (its suffix list, e.g. .ts.net, is not duplicated in this
// package).
func assertPublicURL(parsed *url.URL) error {
	return harvest.AssertFetchable(parsed.String())
}
