package harvestmcp

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/harvest"
)

const (
	maxPageSources = 50
	maxWorks       = 20
	cacheStatusHit = "hit"

	readPageDescription   = `Reads 1–50 web pages as Markdown, input order kept. Call readPage{sources:["https://…"]} — http(s) page URLs only; a paper's landing page is a page too. A DOI, arXiv id, PMID, PMCID, ISBN or findWorks handle goes to readWork; a local path to parseLocalDocuments; a file you want as bytes (PDF, zip, image, audio) to download. Each item returns content (may be truncated), size, cache status, and where the COMPLETE artifact is. A failing item carries its own error and the others still return; an empty extraction is that item's error, never a blank body.`
	parseLocalDescription = `Parses 1–50 local documents (` + harvest.ReadableFormats + `) on this machine into Markdown, input order kept. Call parseLocalDocuments{paths:["/path/to/file.pdf"]}. A web URL goes to readPage; a DOI or other work identifier to readWork. Each item returns content (may be truncated), size and the artifact path; a failing item carries its own error and the others still return.`
	readWorkDescription   = `Reads 1–20 scholarly works — a DOI, arXiv id, PMID, PMCID, ISBN, a paper or book landing URL, or a handle from findWorks — as Markdown, through the repository, mirror or open-access copy that serves it. Call readWork{works:["10.1038/nature14539"]}. A title goes to findWorks first. Each item returns content (may be truncated), the identifiers it read, the route it took, and where the COMPLETE artifact is; a failing item carries its own error and the others still return.`
)

// ReadPageInput is readPage's input.
type ReadPageInput struct {
	Sources  []string          `json:"sources" jsonschema:"1–50 web page URLs (http or https), each read as Markdown in the same order. A DOI, arXiv id, PMID, PMCID, ISBN or findWorks handle goes to readWork; a local path goes to parseLocalDocuments."`
	Refresh  bool              `json:"refresh,omitempty" jsonschema:"Bypass the cache: read the page again and overwrite the cached artifact."`
	OCRLang  string            `json:"ocr_lang,omitempty" jsonschema:"Optional script for OCR of a scanned document: latin, zh, ja, ar, ru or he. By default the document's text layer, /Lang or metadata names it, else Latin; set it when a scan's result is flagged partial as read in Latin. Always a fresh read."`
	SizeOnly bool              `json:"size_only,omitempty" jsonschema:"Read and cache the full content but return no body: only its size and where it is."`
	Headers  map[string]string `json:"headers,omitempty" jsonschema:"Optional request headers (name → value) sent only to the target's own origin; reader services, archives and resolver APIs never receive them. At most 32 headers, 8 KiB; no Host, Content-Length, Transfer-Encoding, Connection, Upgrade, TE, Trailer, Keep-Alive or Proxy-*. A caller header overrides the default of its name."`
}

// ParseLocalInput is parseLocalDocuments' input.
type ParseLocalInput struct {
	Paths    []string `json:"paths" jsonschema:"1–50 local document paths (or file:// URLs), each parsed to Markdown in the same order."`
	SizeOnly bool     `json:"size_only,omitempty" jsonschema:"Parse and cache the full content but return no body: only its size and path."`
	OCRLang  string   `json:"ocr_lang,omitempty" jsonschema:"Optional script for OCR of a scanned document: latin, zh, ja, ar, ru or he. By default the document's text layer, /Lang or metadata names it, else Latin; set it when a scan's result is flagged partial as read in Latin. Always a fresh read."`
}

// ReadWorkInput is readWork's input.
type ReadWorkInput struct {
	Works    []string          `json:"works" jsonschema:"1–20 works: a DOI, arXiv id, PMID, PMCID, ISBN, a paper or book landing URL, or a findWorks handle."`
	Refresh  bool              `json:"refresh,omitempty" jsonschema:"Bypass the cache: read the work again and overwrite the cached artifact."`
	OCRLang  string            `json:"ocr_lang,omitempty" jsonschema:"Optional script for OCR of a scanned document: latin, zh, ja, ar, ru or he. By default the document's text layer, /Lang or metadata names it, else Latin; set it when a scan's result is flagged partial as read in Latin. Always a fresh read."`
	SizeOnly bool              `json:"size_only,omitempty" jsonschema:"Read and cache the full content but return no body: only its size and where it is."`
	Headers  map[string]string `json:"headers,omitempty" jsonschema:"Optional request headers (name → value) sent only to the target's own origin; reader services, archives and resolver APIs never receive them. At most 32 headers, 8 KiB; no Host, Content-Length, Transfer-Encoding, Connection, Upgrade, TE, Trailer, Keep-Alive or Proxy-*. A caller header overrides the default of its name."`
}

// PageItem is one read item of readPage, parseLocalDocuments and readWork.
// Path is left out on the remote server; IDs and Route are readWork's.
type PageItem struct {
	Source     string            `json:"source"`
	Kind       string            `json:"kind,omitempty"`
	Title      string            `json:"title,omitempty"`
	Method     string            `json:"method,omitempty"`
	Status     int               `json:"status,omitempty"`
	Partial    string            `json:"partial"`
	Cached     bool              `json:"cached"`
	Chars      int               `json:"chars,omitempty"`
	Path       string            `json:"path,omitempty"`
	Content    string            `json:"content,omitempty"`
	IDs        map[string]string `json:"ids,omitempty"`
	Route      string            `json:"route,omitempty"`
	Error      string            `json:"error,omitempty"`
	RetryAfter string            `json:"retry_after,omitempty"`
}

// PagesOutput is the typed output of the three read tools.
type PagesOutput struct {
	Items []PageItem `json:"items"`
}

// readRequest is one read tool's call: its options, the wrong-tool check
// each source passes first, and readWork's identifier decoration.
type readRequest struct {
	options  harvest.FetchOptions
	misroute func(source string) string
	work     bool
	headers  harvest.CallerHeaders // the caller's headers, validated at entry
}

func (service *Service) readPage(
	ctx context.Context, _ *mcp.CallToolRequest, input ReadPageInput,
) (*mcp.CallToolResult, PagesOutput, error) {
	if len(input.Sources) < 1 || len(input.Sources) > maxPageSources {
		return nil, PagesOutput{}, fmt.Errorf("sources must contain 1-%d items", maxPageSources)
	}
	ocrLang, err := harvest.ParseOCRLang(input.OCRLang)
	if err != nil {
		return nil, PagesOutput{}, err
	}
	headers, err := harvest.ParseCallerHeaders(input.Headers)
	if err != nil {
		return nil, PagesOutput{}, err
	}
	return service.readMany(ctx, input.Sources, readRequest{
		headers:  headers,
		options:  harvest.FetchOptions{Refresh: input.Refresh, SizeOnly: input.SizeOnly, OCRLang: ocrLang},
		misroute: pageMisroute,
	})
}

func (service *Service) parseLocalDocuments(
	ctx context.Context, _ *mcp.CallToolRequest, input ParseLocalInput,
) (*mcp.CallToolResult, PagesOutput, error) {
	if len(input.Paths) < 1 || len(input.Paths) > maxPageSources {
		return nil, PagesOutput{}, fmt.Errorf("paths must contain 1-%d items", maxPageSources)
	}
	ocrLang, err := harvest.ParseOCRLang(input.OCRLang)
	if err != nil {
		return nil, PagesOutput{}, err
	}
	return service.readMany(ctx, input.Paths, readRequest{
		options:  harvest.FetchOptions{SizeOnly: input.SizeOnly, OCRLang: ocrLang},
		misroute: localMisroute,
	})
}

func (service *Service) readWork(
	ctx context.Context, _ *mcp.CallToolRequest, input ReadWorkInput,
) (*mcp.CallToolResult, PagesOutput, error) {
	if len(input.Works) < 1 || len(input.Works) > maxWorks {
		return nil, PagesOutput{}, fmt.Errorf("works must contain 1-%d items", maxWorks)
	}
	ocrLang, err := harvest.ParseOCRLang(input.OCRLang)
	if err != nil {
		return nil, PagesOutput{}, err
	}
	headers, err := harvest.ParseCallerHeaders(input.Headers)
	if err != nil {
		return nil, PagesOutput{}, err
	}
	return service.readMany(ctx, input.Works, readRequest{
		headers:  headers,
		options:  harvest.FetchOptions{Refresh: input.Refresh, SizeOnly: input.SizeOnly, OCRLang: ocrLang},
		misroute: workMisroute,
		work:     true,
	})
}

// readMany fans the sources out (8 at a time, per-item isolation in
// fetchOne) and returns one Content text and one typed item per source.
func (service *Service) readMany(
	ctx context.Context, sources []string, request readRequest,
) (*mcp.CallToolResult, PagesOutput, error) {
	contents := make([]string, len(sources))
	items := make([]PageItem, len(sources))
	var wait sync.WaitGroup
	semaphore := make(chan struct{}, 8)
	for index, source := range sources {
		wait.Add(1)
		go service.fetchOne(ctx, semaphore, &wait, index, source, request, contents, items)
	}
	wait.Wait()
	// Every item failed: the call is an error, so a failed batch never reads
	// as a result; one item that read keeps the batch a result.
	result := &mcp.CallToolResult{IsError: true}
	for index, text := range contents {
		result.Content = append(result.Content, &mcp.TextContent{Text: text})
		if items[index].Error == "" {
			result.IsError = false
		}
	}
	return result, PagesOutput{Items: items}, nil
}

// pageItem is the typed item of one published read result.
func (service *Service) pageItem(source string, result harvest.Result, work bool) PageItem {
	meta := frontmatter(result.Path)
	item := PageItem{
		Source:     source,
		Kind:       result.Kind,
		Title:      valueOr(meta["title"], firstHeading(result.Content)),
		Method:     harvest.PublicMethod(result.Method),
		Status:     result.HTTPStatus,
		Partial:    result.Partial,
		Cached:     result.CacheStatus == cacheStatusHit,
		Chars:      result.Chars,
		Path:       result.Path,
		Content:    result.Content,
		Error:      result.Error,
		RetryAfter: result.RetryAfter,
	}
	if service.runtime.Remote {
		item.Path = ""
	}
	if work {
		item.IDs = workIDs(source)
		if item.Error == "" {
			item.Route = item.Method
		}
	}
	return item
}

func firstHeading(content string) string {
	for _, line := range strings.SplitN(content, "\n", 40) {
		if title, ok := strings.CutPrefix(strings.TrimSpace(line), "# "); ok {
			return strings.TrimSpace(title)
		}
	}
	return ""
}

var workIDPatterns = []struct {
	name    string
	pattern *regexp.Regexp
}{
	{"doi", regexp.MustCompile(`(?i)^(?:doi:\s*|https?://(?:dx\.)?doi\.org/)?(10\.\d{4,9}/\S+)$`)},
	{"arxiv", regexp.MustCompile(`(?i)^(?:arxiv:\s*|https?://arxiv\.org/abs/)(\d{4}\.\d{4,5}(?:v\d+)?)$`)},
	{"pmcid", regexp.MustCompile(`(?i)^(?:pmcid:\s*)?(PMC\d+)$`)},
	{"pmid", regexp.MustCompile(`(?i)^pmid:\s*(\d{1,9})$`)},
	{"isbn", regexp.MustCompile(`(?i)^isbn:?\s*((?:97[89][- ]?)?(?:\d[- ]?){9}[\dX])$`)},
}

// workIDs names the scholarly identifiers a source spells, or nil.
func workIDs(source string) map[string]string {
	s := strings.TrimSpace(source)
	for _, id := range workIDPatterns {
		if match := id.pattern.FindStringSubmatch(s); match != nil {
			return map[string]string{id.name: match[1]}
		}
	}
	return nil
}

// workNoun is the readable name of the identifier a source spells: "a DOI".
func workNoun(source string) string {
	for name := range workIDs(source) {
		return map[string]string{
			"doi": "a DOI", "arxiv": "an arXiv id", "pmcid": "a PMCID", "pmid": "a PMID", "isbn": "an ISBN",
		}[name]
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(source)), "harvest:") {
		return "a findWorks handle"
	}
	return ""
}

func isWebURL(source string) bool {
	lower := strings.ToLower(strings.TrimSpace(source))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func isLocalInput(source string) bool {
	s := strings.TrimSpace(source)
	return filepath.IsAbs(s) || strings.HasPrefix(strings.ToLower(s), "file://") ||
		strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") || strings.HasPrefix(s, "~/")
}

// pageMisroute is readPage's wrong-tool check: a work identifier names
// readWork, a local path names parseLocalDocuments. A landing URL is a page.
func pageMisroute(source string) string {
	switch {
	case workNoun(source) != "":
		return "this is " + workNoun(source) + "; read it with `readWork`."
	case isLocalInput(source):
		return "this is a local path; read it with `parseLocalDocuments`."
	case !isWebURL(source):
		return "this is not a web URL; readPage reads http(s) pages — a title goes to `findWorks`."
	}
	return ""
}

// localMisroute is parseLocalDocuments' wrong-tool check.
func localMisroute(source string) string {
	switch {
	case workNoun(source) != "":
		return "this is " + workNoun(source) + "; read it with `readWork`."
	case isWebURL(source):
		return "this is a URL; read it with `readPage`."
	}
	return ""
}

// workMisroute is readWork's wrong-tool check: a landing URL is a work.
func workMisroute(source string) string {
	switch {
	case workNoun(source) != "", isWebURL(source):
		return ""
	case isLocalInput(source):
		return "this is a local path; read it with `parseLocalDocuments`."
	case strings.ContainsAny(strings.TrimSpace(source), " \t"):
		return "this looks like a title; find it with `findWorks`, then read its handle with `readWork`."
	}
	return ""
}
