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

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/harvest"
	"github.com/rezzminator/professor/pfm/internal/harvestpy"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const (
	defaultInlineChars  = 50000
	jsonSchemaBoolean   = "boolean"
	jsonSchemaObject    = "object"
	jsonSchemaString    = "string"
	jsonPropertyQuery   = "query"
	jsonPropertySource  = "source"
	jsonPropertySources = "sources"
	maxFetchSources     = 50
	maxImageSources     = 50
	maxSearchResults    = 20
	maxFindResults      = 25
	maxCacheResults     = 1000
)

// Runtime is the resolved harvester.config.json plus machine-local paths. The
// MCP service never reads a machine config file or the process environment
// itself; the command passes this already-resolved runtime in.
type Runtime struct {
	Env        paths.Env
	Clock      clock.Clock
	Runner     deps.Runner
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
func NewConfiguredHarvester(version string, runtime Runtime) (*Service, error) {
	if runtime.Env == nil {
		runtime.Env = paths.OSEnv{}
	}
	if runtime.Clock == nil {
		runtime.Clock = clock.Real
	}
	if runtime.Runner == nil {
		runtime.Runner = obs.Runner(deps.RealRunner{})
	}
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
		Instructions: serverInstructions(runtimeSearchEnabled(runtime)),
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
	if runtime.Env == nil {
		runtime.Env = paths.OSEnv{}
	}
	if runtime.Clock == nil {
		runtime.Clock = clock.Real
	}
	if runtime.Runner == nil {
		runtime.Runner = obs.Runner(deps.RealRunner{})
	}
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
			Runner:    runtime.Runner,
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
		runner:      runtime.Runner,
		proxyURL:    runtime.ProxyURL,
	}
	browser := runtime.Browser
	harvester, err := harvest.New(harvest.Options{
		CacheDir:              runtime.CacheDir,
		Clock:                 runtime.Clock,
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
		SearchAvailable:       runtimeSearchEnabled(runtime),
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
	if root := runtimeEnv(runtime).Get("PFM_HARVEST_ROOT"); root != "" {
		return root, nil
	}
	home := runtime.Home
	if home == "" {
		var err error
		home, err = runtimeEnv(runtime).Home()
		if err != nil {
			return "", fmt.Errorf("resolve Harvester Python home: %w", err)
		}
	}
	return filepath.Join(home, ".local", "state", "pfm", "harvest-python"), nil
}

func runtimeEnv(runtime Runtime) paths.Env {
	if runtime.Env != nil {
		return runtime.Env
	}
	return paths.OSEnv{}
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
	runner      deps.Runner
	proxyURL    string
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
	return obs.Handler("harvester-mcp", mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return service.server },
		&mcp.StreamableHTTPOptions{
			JSONResponse:               true,
			Stateless:                  false,
			DisableLocalhostProtection: false,
		},
	))
}

// The SDK's IOTransport takes ownership of ReadCloser/WriteCloser values and
// closes both at connection shutdown. RunStdio accepts the older, deliberately
// non-owning io.Reader/io.Writer contract, so adapt them with no-op closers.
type nopReaderCloser struct{ io.Reader }

func (nopReaderCloser) Close() error { return nil }

type nopWriterCloser struct{ io.Writer }

func (nopWriterCloser) Close() error { return nil }

// discardOutput adapts a handler that also returns a typed output to the untyped Out register() wants for five of its six tools: only result.Content ever reaches the wire (searchCache's own two-return shape keeps its inline handler below).
func discardOutput[In, Out any](
	handler func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error),
) mcp.ToolHandlerFor[In, any] {
	return func(ctx context.Context, request *mcp.CallToolRequest, input In) (*mcp.CallToolResult, any, error) {
		result, _, err := handler(ctx, request, input)
		return result, nil, err
	}
}

func (service *Service) register() {
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true}
	mcp.AddTool(
		service.server,
		&mcp.Tool{Name: "fetch", Description: fetchDescription, InputSchema: fetchInputSchema(), Annotations: readOnly},
		obs.Tool("fetch", discardOutput(service.fetch)),
	)
	mcp.AddTool(
		service.server,
		&mcp.Tool{
			Name:        "findWorks",
			Description: findDescription,
			InputSchema: findInputSchema(),
			Annotations: readOnly,
		},
		obs.Tool("findWorks", discardOutput(service.findWorks)),
	)
	if runtimeSearchEnabled(service.runtime) {
		mcp.AddTool(
			service.server,
			&mcp.Tool{
				Name:        "search",
				Description: searchDescription,
				InputSchema: searchInputSchema(),
				Annotations: readOnly,
			},
			obs.Tool("search", discardOutput(service.search)),
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
		obs.Tool("fetchImage", discardOutput(service.fetchImage)),
	)
	mcp.AddTool(
		service.server,
		&mcp.Tool{
			Name:        "archive",
			Description: archiveDescription,
			InputSchema: archiveInputSchema(),
			Annotations: readOnly,
		},
		obs.Tool("archive", discardOutput(service.archive)),
	)
	mcp.AddTool(
		service.server,
		&mcp.Tool{
			Name:        "searchCache",
			Description: cacheDescription,
			InputSchema: cacheInputSchema(),
			Annotations: readOnly,
		},
		obs.Tool(
			"searchCache",
			func(ctx context.Context, request *mcp.CallToolRequest, input CacheInput) (*mcp.CallToolResult, any, error) {
				result, err := service.searchCache(ctx, request, input)
				return result, nil, err
			},
		),
	)
	service.server.AddPrompt(
		&mcp.Prompt{
			Name:        "fetch",
			Description: "Fetch a URL or local path and convert its contents to markdown",
			Arguments:   []*mcp.PromptArgument{{Name: "url", Description: "URL or path to fetch", Required: true}},
		},
		obs.Prompt("fetch", service.fetchPrompt),
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
		Items:       &jsonschema.Schema{Type: jsonSchemaString},
		MinItems:    &minimum,
		MaxItems:    &maximum,
	}
}

func fetchInputSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: jsonSchemaObject, Required: []string{jsonPropertySources},
		Properties: map[string]*jsonschema.Schema{
			jsonPropertySources: arraySchema(
				"1–50 things to fetch, each returned as clean Markdown in the SAME order. Each is a LOCATION or an UNAMBIGUOUS identifier of a DOCUMENT: URL / local path / file://; DOI; ISBN; PMID / PMCID; harvest: handle. Use a DIFFERENT tool for a TITLE (findWorks), an IMAGE (fetchImage), or an archive (archive). A failing item returns a descriptive per-item error and the rest still return.",
				1,
				50,
			),
			"refresh": {
				Type:        jsonSchemaBoolean,
				Description: "Force a fresh fetch: bypass the cache entirely, re-download, overwrite the cached artifact, and return the NEW content.",
				Default:     defaultJSON("false"),
			},
			"size_only": {
				Type:        jsonSchemaBoolean,
				Description: "When true, fetch and cache the full content but return NO body—just {size, chars, path}; full content remains cached at path.",
				Default:     defaultJSON("false"),
			},
		},
	}
}

func findInputSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:     jsonSchemaObject,
		Required: []string{jsonPropertyQuery},
		Properties: map[string]*jsonschema.Schema{
			jsonPropertyQuery: {
				Type:        jsonSchemaString,
				Description: "A paper or book TITLE, or a free-text bibliographic query. Returns a ranked list of candidate works with a fetch handle; pick one and pass it to fetch.",
			},
			"limit": numberSchema("Maximum number of candidate works to return.", 1, 25, 8),
		},
	}
}

func searchInputSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:     jsonSchemaObject,
		Required: []string{jsonPropertyQuery},
		Properties: map[string]*jsonschema.Schema{
			jsonPropertyQuery: {Type: jsonSchemaString, Description: "The web search query."},
			"count":           numberSchema("Maximum number of results to return.", 1, 20, 8),
			"lang": {
				Type:        jsonSchemaString,
				Description: "Optional language/locale to bias the search (e.g. 'zh', 'ja', 'pt-BR'). Set it to reach a NON-English literature.",
			},
			"engines": {
				Type:        jsonSchemaString,
				Description: "Optional comma-separated SearXNG engines to restrict to (e.g. 'google,brave' or 'naver,yahoo'). Omit for the default aggregated set.",
			},
		},
	}
}

func imageInputSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:     jsonSchemaObject,
		Required: []string{jsonPropertySources},
		Properties: map[string]*jsonschema.Schema{
			jsonPropertySources: arraySchema(
				"1–50 image URLs or local image paths. Each is downloaded into the type-partitioned cache and its LOCAL FILE PATH is returned in order — images are NOT OCR'd.",
				1,
				50,
			),
		},
	}
}

func archiveInputSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:     jsonSchemaObject,
		Required: []string{jsonPropertySource},
		Properties: map[string]*jsonschema.Schema{
			jsonPropertySource: {
				Type:        jsonSchemaString,
				Description: "URL or local path of a .zip / .tar(.gz/.bz2/.xz) / .7z / .rar archive.",
			},
			"member": {
				Types:       []string{jsonSchemaString, "null"},
				Description: "Omit to get the SAFE member listing (names + sizes; nothing is extracted). Give one member name from that listing to fetch just that member, converted to Markdown.",
			},
		},
	}
}

func cacheInputSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:     jsonSchemaObject,
		Required: []string{"pattern"},
		Properties: map[string]*jsonschema.Schema{
			"pattern": {
				Type:        jsonSchemaString,
				Description: "Regex pattern to search across every cached markdown body in the cache.",
			},
			"max_results": numberSchema("Maximum number of matching cached pages to return.", 1, 1000, 50),
			"ignore_case": {
				Type:        jsonSchemaBoolean,
				Description: "Case-insensitive search.",
				Default:     defaultJSON("true"),
			},
		},
	}
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
		go service.fetchOne(ctx, semaphore, &wait, index, source, input, contents, items)
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
		return nil, FindOutput{}, errors.New("work discovery failed; retry later or fetch an exact identifier")
	}
	candidates, err = service.harvester.PublicCandidates(candidates)
	if err != nil {
		fmt.Fprintf(os.Stderr, "harvester export candidates: %v\n", err)
		return nil, FindOutput{}, errors.New("could not prepare the discovered works for retrieval; retry later")
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: renderFind(input.Query, candidates)}},
	}, FindOutput{Candidates: candidates}, nil
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
		// err.Error() is the FULL diagnostic (search.go:56-58) — may carry a
		// backend URL with userinfo — so it goes through the scrubbed activity
		// log, never raw to stderr; renderSearchFailure is the one safe
		// rendering (SafeMessage() when it's a *SearchBackendError, the
		// verbatim sentinel for a configuration state) both stderr and the
		// tool result share.
		obs.Logger(obs.Component(ctx, "mcp")).Warn("harvester.search.failed", obs.FieldErr, err.Error())
		safe := renderSearchFailure(err)
		fmt.Fprintln(os.Stderr, safe)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: safe}},
			IsError: true,
		}, SearchOutput{}, nil
	}
	for index := range results {
		results[index].Engine = ""
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: renderSearch(input.Query, results, "")}},
	}, SearchOutput{Results: results}, nil
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
		go service.imageOne(ctx, semaphore, &wait, index, source, items, contents)
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
	err := resolveErr
	if resolveErr != nil {
		result.Error, result.ErrorKind = resolveErr.Error(), "policy"
	} else {
		result, err = service.harvester.Archive(ctx, resolved, input.Member)
	}
	result = service.harvester.PublicResult(input.Source, result, false)
	text := service.describeFetch(input.Source, result, false)
	if err == nil && result.Error == "" && input.Member == "" {
		// input.Source is raw (a local path, a file: URL); PublicArchiveListing
		// applies the SAME redaction PublicResult above already applied to
		// result's own source, and it is the one archive-listing renderer in
		// the tree (L2-F19) — the live answer and the exported file cannot
		// drift apart again.
		text = service.harvester.PublicArchiveListing(input.Source, result.Members)
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}, ArchiveOutput{Result: result}, nil
}

func (service *Service) searchCache(
	_ context.Context,
	_ *mcp.CallToolRequest,
	input CacheInput,
) (*mcp.CallToolResult, error) {
	if strings.TrimSpace(input.Pattern) == "" {
		return nil, errors.New("pattern must not be empty")
	}
	limit := input.MaxResults
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > maxCacheResults {
		return nil, fmt.Errorf("max_results must be between 1 and %d", maxCacheResults)
	}
	ignore := true
	if input.IgnoreCase != nil {
		ignore = *input.IgnoreCase
	}
	cacheHits, err := service.harvester.SearchCachePublic(input.Pattern, limit, ignore)
	if err != nil {
		fmt.Fprintf(os.Stderr, "harvester searchCache: %v\n", err)
		return nil, errors.New("cache search failed; check the expression or retry later")
	}
	hits := make([]CacheHit, 0, len(cacheHits))
	for _, hit := range cacheHits {
		hits = append(hits, CacheHit{URL: hit.URL, Path: hit.Path, Matches: hit.Matches, Sample: hit.Sample})
	}
	lines := make([]string, 0, len(hits)+2)
	if len(hits) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(
				"No cached pages match /%s/. This only searches pages already fetched — it does not search the web; %s",
				input.Pattern,
				harvest.SearchHint(runtimeSearchEnabled(service.runtime),
					"use `search` or `fetch` a source first.", "fetch a source first."),
			)}},
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
