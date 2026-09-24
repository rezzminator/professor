// Package harvestmcp exposes the Harvester's stable six-tool MCP surface.
//
// Transport, cache and policy remain in internal/harvest. This package only
// adapts those operations to MCP and preserves the Python server's receipts.
package harvestmcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	goRuntime "runtime"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/harvest"
	"github.com/rezzminator/professor/pfm/internal/harvestpy"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const (
	defaultInlineChars = 50000
	jsonSchemaBoolean  = "boolean"
	jsonSchemaObject   = "object"
	jsonSchemaString   = "string"
	jsonPropertyQuery  = "query"
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
	// Remote marks the external gateway's service: read takes no `files` there
	// (its schema drops the field), no result carries a server path, and download_file answers a signed
	// expiring /files URL plus a resource_link served by the download
	// resource template.
	Remote bool
	// MaxDownloadBytes is harvest.maxDownloadBytes; MaxResourceBytes is
	// harvest.maxResourceBytes, the largest blob resources/read sends. 0 = default.
	MaxDownloadBytes int64
	MaxResourceBytes int64
}

// Service is one independent Harvester MCP server.
type Service struct {
	server    *mcp.Server
	harvester *harvest.Harvester
	resolver  *harvest.Resolver
	runtime   Runtime
	worker    *harvestpy.Converter
	downloads *downloadStore
	// links signs remote download URLs; set only by NewRemote (nil elsewhere).
	links *downloadLinks
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
		Instructions: serverInstructions(runtimeSearchEnabled(runtime), runtime.Remote),
	})
	resolver := &harvest.Resolver{
		Client: resolverClient, ContactEmail: runtime.ContactEmail, GoogleBooksAPIKey: runtime.GoogleBooksAPIKey,
		CoreAPIKey: runtime.CoreAPIKey, SemanticScholarAPIKey: runtime.SemanticScholarAPIKey,
		IPFSCatalogURL:   runtime.IPFSCatalogURL,
		DOIViewerURL:     runtime.DOIViewerURL,
		MD5CatalogURL:    runtime.MD5CatalogURL,
		GoogleScholarURL: runtime.GoogleScholarURL,
	}
	service := &Service{
		server:    server,
		harvester: h,
		resolver:  resolver,
		runtime:   runtime,
		worker:    worker,
		downloads: newDownloadStore(),
	}
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
		MaxDownloadBytes:      runtime.MaxDownloadBytes,
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
) (string, error) {
	extension := strings.TrimPrefix(filepath.Ext(source), ".")
	if extension == "" || extension == source {
		extension = kind
	}
	return converter.convertScratch(
		ctx, "conversion", "pfm-harvest-input-", extension, body,
		harvestpy.Request{Kind: kind, Source: source},
	)
}

// ConvertOCR forces ONE Tesseract pass for this document only — the dispatch's
// last-resort rung for scanned PDFs whose text layer converts empty.
func (converter pythonConverter) ConvertOCR(
	ctx context.Context,
	kind, source string,
	body []byte,
) (string, error) {
	return converter.convertScratch(
		ctx, "OCR", "pfm-harvest-ocr-", strings.TrimPrefix(filepath.Ext(source), "."), body,
		harvestpy.Request{Kind: kind, Source: source, OCR: true},
	)
}

// ConvertFullDOM converts an HTML page's whole DOM, boilerplate included —
// the recall gate's fallback when main-content extraction kept too little of
// the page (harvest.FullDOMConverter).
func (converter pythonConverter) ConvertFullDOM(
	ctx context.Context,
	source string,
	body []byte,
) (string, error) {
	return converter.convertScratch(
		ctx, "full-DOM conversion", "pfm-harvest-fulldom-", "html", body,
		harvestpy.Request{Kind: "html", Source: source, FullDOM: true},
	)
}

// convertScratch writes body to a private scratch file input.{extension},
// hands the worker request with that path, and removes the scratch; label
// names the scratch in every error, a failed cleanup included.
func (converter pythonConverter) convertScratch(
	ctx context.Context,
	label, prefix, extension string,
	body []byte,
	request harvestpy.Request,
) (markdown string, returnErr error) {
	directory, err := os.MkdirTemp("", prefix)
	if err != nil {
		return "", fmt.Errorf("create %s scratch: %w", label, err)
	}
	defer func() {
		if cleanupErr := os.RemoveAll(directory); cleanupErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("remove %s scratch: %w", label, cleanupErr))
		}
	}()
	request.Path = filepath.Join(directory, "input."+extension)
	request.OCRLang = harvest.OCRLangFrom(ctx)
	if err := os.WriteFile(request.Path, body, 0o600); err != nil {
		return "", fmt.Errorf("write %s scratch: %w", label, err)
	}
	result, convertErr := converter.worker.Convert(ctx, request)
	if convertErr != nil {
		return "", convertErr
	}
	return result.Markdown, nil
}

var _ harvest.FullDOMConverter = pythonConverter{}

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

// register adds the four tools (search_web only with a search backend; read
// on the remote server without `files` in its schema) and, on the remote
// server, the download resource template. Each tool returns its output
// struct: the SDK derives the output schema from it and sends
// structuredContent beside the readable Content text. RegisteredToolNames
// (toolnames.go) moves with it.
func (service *Service) register() {
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true}
	readTool := &mcp.Tool{Name: toolRead, Description: readDescription, Annotations: readOnly}
	if service.runtime.Remote {
		readTool.Description = readRemoteDescription
		readTool.InputSchema = remoteReadSchema()
	}
	mcp.AddTool(service.server, readTool, obs.Tool(toolRead, service.read))
	mcp.AddTool(service.server,
		&mcp.Tool{Name: toolDownloadFile, Description: downloadFileDescription, Annotations: readOnly},
		obs.Tool(toolDownloadFile, service.downloadFile))
	mcp.AddTool(service.server,
		&mcp.Tool{Name: toolSearchLiterature, Description: searchLiteratureDescription, Annotations: readOnly},
		obs.Tool(toolSearchLiterature, service.searchLiterature))
	if runtimeSearchEnabled(service.runtime) {
		mcp.AddTool(service.server,
			&mcp.Tool{Name: toolSearchWeb, Description: searchWebDescription, Annotations: readOnly},
			obs.Tool(toolSearchWeb, service.searchWeb))
	}
	if service.runtime.Remote {
		service.server.AddResourceTemplate(&mcp.ResourceTemplate{
			Name:        "download",
			URITemplate: downloadURITemplate,
			Description: "A file the download_file tool fetched, by its sha256; resources/read answers its bytes as a blob.",
		}, service.readDownload)
	}
}
