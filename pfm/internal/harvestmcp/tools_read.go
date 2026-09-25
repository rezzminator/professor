package harvestmcp

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/harvest"
)

const (
	maxReadItems    = 50
	maxPublications = 20
	cacheStatusHit  = "hit"

	// The read tool's three source fields, in the order its input, its
	// structured output and its text Content all keep.
	fieldURLs         = "urls"
	fieldFiles        = "files"
	fieldPublications = "publications"

	readDescriptionHead = `Reads web pages, local documents and scholarly works as Markdown in one call, each kind in its own array, input order kept: 1–50 items in total. `
	readDescriptionURLs = `urls: http(s) pages, each read as a page (a PDF or document URL is parsed; a paper's landing page here reads the page). `
	readDescriptionPubs = `publications (at most 20): a DOI, arXiv id, PMID, PMCID, ISBN, a harvester_search_literature handle, or a paper or book landing URL, each read as its full text through the repository, mirror or open-access copy that serves it. `
	readDescriptionTail = `A title goes to harvester_search_literature first; a file you want as bytes (PDF, zip, image, audio) goes to harvester_download_file. The answer groups the items by field in the same order; each item returns content (may be truncated; include_content:false returns none), chars, tokens, via (how it was read), gaps (why the artifact is incomplete, empty when complete) and where the COMPLETE artifact is. A misplaced item names the field it belongs in; a failing item carries its own error and the others still return.`

	readDescription = readDescriptionHead +
		`Call harvester_read{urls:["https://…"], files:["/path/to/file.pdf"], publications:["10.1038/nature14539"]}. ` +
		readDescriptionURLs +
		`files: local documents (` + harvest.ReadableFormats + `) or file:// URLs on this machine, each parsed. ` +
		readDescriptionPubs + readDescriptionTail
	readRemoteDescription = readDescriptionHead +
		`Call harvester_read{urls:["https://…"], publications:["10.1038/nature14539"]}. ` +
		readDescriptionURLs + readDescriptionPubs +
		`This server reads no local files. ` + readDescriptionTail
)

// ReadInput is read's input: every source in the field that names its kind.
type ReadInput struct {
	URLs           []string          `json:"urls,omitempty" jsonschema:"Web page URLs (http or https), each read as a page; a PDF or document URL is parsed. An identifier goes in publications, a local path in files."`
	Files          []string          `json:"files,omitempty" jsonschema:"Local document paths (or file:// URLs) on this machine, each parsed to Markdown. A web URL goes in urls."`
	Publications   []string          `json:"publications,omitempty" jsonschema:"At most 20 works: a DOI, arXiv id, PMID, PMCID, ISBN, a harvester_search_literature handle, or a paper or book landing URL, each read as its full text."`
	Refresh        bool              `json:"refresh,omitempty" jsonschema:"Bypass the cache: read every item again and overwrite its cached artifact."`
	IncludeContent *bool             `json:"include_content,omitempty" jsonschema:"Default true. false: read and cache the full content but return no body, only its size and where it is."`
	OCRLanguage    string            `json:"ocr_language,omitempty" jsonschema:"Optional script for OCR of a scanned document: latin, zh, ja, ar, ru or he. By default the document's text layer, /Lang or metadata names it, else Latin; set it when a scan's gaps say it was read in Latin. Always a fresh read."`
	Headers        map[string]string `json:"headers,omitempty" jsonschema:"Optional request headers (name → value) sent only to a url's own origin and a publication's landing origin, never with files; reader services, archives and resolver APIs never receive them. At most 32 headers, 8 KiB; no Host, Content-Length, Transfer-Encoding, Connection, Upgrade, TE, Trailer, Keep-Alive or Proxy-*. A caller header overrides the default of its name."`
}

// ReadItem is one read item. Path is left out on the remote server; IDs are
// a publication's.
type ReadItem struct {
	Source     string            `json:"source"`
	Kind       string            `json:"kind,omitempty"`
	Title      string            `json:"title,omitempty"`
	Via        string            `json:"via,omitempty"`
	Status     int               `json:"status,omitempty"`
	Gaps       []string          `json:"gaps"`
	Cached     bool              `json:"cached"`
	Chars      int               `json:"chars,omitempty"`
	Tokens     int               `json:"tokens,omitempty"`
	Path       string            `json:"path,omitempty"`
	Content    string            `json:"content,omitempty"`
	IDs        map[string]string `json:"ids,omitempty"`
	RetryAfter string            `json:"retry_after,omitempty"`
	Error      string            `json:"error,omitempty"`
}

// ReadOutput is read's typed output: one array per input field, each in the
// input's order, an empty one omitted.
type ReadOutput struct {
	URLs         []ReadItem `json:"urls,omitempty"`
	Files        []ReadItem `json:"files,omitempty"`
	Publications []ReadItem `json:"publications,omitempty"`
}

// readJob is one source of a read call and the field it came in.
type readJob struct {
	field  string
	source string
}

// readRequest is one read call's shared options.
type readRequest struct {
	options harvest.FetchOptions
	headers harvest.CallerHeaders // the caller's headers, validated at entry
}

// remoteReadSchema is read's input schema on the remote gateway: the same
// fields without `files`. It stays open to unlisted fields so a remote call
// that sends `files` reaches read, which refuses it by name.
func remoteReadSchema() *jsonschema.Schema {
	schema, err := jsonschema.For[ReadInput](nil)
	if err != nil {
		panic(fmt.Sprintf("harvestmcp: infer the read input schema: %v", err))
	}
	delete(schema.Properties, fieldFiles)
	schema.AdditionalProperties = nil
	return schema
}

func (service *Service) read(
	ctx context.Context, _ *mcp.CallToolRequest, input ReadInput,
) (*mcp.CallToolResult, ReadOutput, error) {
	if service.runtime.Remote && len(input.Files) > 0 {
		return nil, ReadOutput{}, errors.New("files is not available on the remote server: it cannot read a file on " +
			"your machine; send a web URL in urls or an identifier in publications, or read the file with a local harvester")
	}
	total := len(input.URLs) + len(input.Files) + len(input.Publications)
	switch {
	case total == 0:
		return nil, ReadOutput{}, errors.New("read needs at least one item in urls, files or publications")
	case total > maxReadItems:
		return nil, ReadOutput{}, fmt.Errorf(
			"read takes at most %d items in total across urls, files and publications; this call sent %d",
			maxReadItems, total)
	case len(input.Publications) > maxPublications:
		return nil, ReadOutput{}, fmt.Errorf("publications takes at most %d items; this call sent %d",
			maxPublications, len(input.Publications))
	}
	ocrLang, err := harvest.ParseOCRLang(input.OCRLanguage)
	if err != nil {
		return nil, ReadOutput{}, err
	}
	headers, err := harvest.ParseCallerHeaders(input.Headers)
	if err != nil {
		return nil, ReadOutput{}, err
	}
	jobs := make([]readJob, 0, total)
	for _, group := range []struct {
		field   string
		sources []string
	}{{fieldURLs, input.URLs}, {fieldFiles, input.Files}, {fieldPublications, input.Publications}} {
		for _, source := range group.sources {
			jobs = append(jobs, readJob{field: group.field, source: source})
		}
	}
	sizeOnly := input.IncludeContent != nil && !*input.IncludeContent
	return service.readMany(ctx, jobs, readRequest{
		headers: headers,
		options: harvest.FetchOptions{Refresh: input.Refresh, SizeOnly: sizeOnly, OCRLang: ocrLang},
	})
}

// readMany fans every job out at once (8 at a time, per-item isolation in
// fetchOne) and answers the items grouped by field.
func (service *Service) readMany(
	ctx context.Context, jobs []readJob, request readRequest,
) (*mcp.CallToolResult, ReadOutput, error) {
	contents := make([]string, len(jobs))
	items := make([]ReadItem, len(jobs))
	var wait sync.WaitGroup
	semaphore := make(chan struct{}, 8)
	for index, job := range jobs {
		wait.Add(1)
		go service.fetchOne(ctx, semaphore, &wait, index, workJob(job), request, contents, items)
	}
	wait.Wait()
	for index, job := range jobs {
		if work := workJob(job); work.source != job.source {
			// The answer echoes the caller's source, not the id it was read as.
			items[index].Source = job.source
			contents[index] = strings.Replace(contents[index], work.source, job.source, 1)
		}
	}
	result, output := renderReadGroups(jobs, contents, items)
	return result, output, nil
}

// workJob is the job a publication is read as: an arxiv.org abs or pdf URL
// there names its work, so it reads as that arXiv id (a URL in urls stays the
// page). Its URL form holds only characters that need no JSON escaping, so
// readMany's echo swap keeps a receipt valid JSON.
func workJob(job readJob) readJob {
	if job.field == fieldPublications {
		if id := harvest.ArXivPageID(job.source); id != "" {
			return readJob{field: job.field, source: "arXiv:" + id}
		}
	}
	return job
}

// readItem is the typed item of one published read result.
func (service *Service) readItem(job readJob, result harvest.Result, includeContent bool) ReadItem {
	meta := frontmatter(result.Path)
	tokens := result.Tokens
	if value, err := strconv.Atoi(meta["token_count"]); err == nil {
		tokens = value
	}
	item := ReadItem{
		Source:     job.source,
		Kind:       result.Kind,
		Title:      valueOr(meta["title"], firstHeading(result.Content)),
		Via:        harvest.PublicMethod(result.Method),
		Status:     result.HTTPStatus,
		Gaps:       harvest.PublicGaps(result.Partial),
		Cached:     result.CacheStatus == cacheStatusHit,
		Chars:      result.Chars,
		Tokens:     tokens,
		Path:       result.Path,
		Error:      result.Error,
		RetryAfter: result.RetryAfter,
	}
	if includeContent {
		item.Content = result.Content
	}
	if service.runtime.Remote {
		item.Path = ""
	}
	if job.field == fieldPublications {
		item.IDs = workIDs(job.source)
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
	{"pmcid", regexp.MustCompile(`(?i)^(?:pmcid:\s*)?(PMC\d+)$`)},
	{"pmid", regexp.MustCompile(`(?i)^pmid:\s*(\d{1,9})$`)},
	{"isbn", regexp.MustCompile(`(?i)^isbn:?\s*((?:97[89][- ]?)?(?:\d[- ]?){9}[\dX])$`)},
}

// workIDs names the scholarly identifiers a source spells, or nil.
func workIDs(source string) map[string]string {
	s := strings.TrimSpace(source)
	if id := harvest.ArXivID(s); id != "" { // harvest reads the same forms as the arXiv DOI
		return map[string]string{"arxiv": id}
	}
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
		return "a harvester_search_literature handle"
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

// misplaced is read's per-item field check: a source in the wrong field
// names the field it belongs in, and only that item fails. A landing URL is
// valid in both urls (the page) and publications (the work).
func misplaced(field, source string, remote bool) string {
	filesField := "put it in files"
	if remote {
		filesField = "this server reads no local files; read it with a local harvester"
	}
	switch field {
	case fieldURLs:
		switch {
		case workNoun(source) != "":
			return "this is " + workNoun(source) + "; put it in publications."
		case isLocalInput(source):
			return "this is a local path; " + filesField + "."
		case !isWebURL(source):
			return "this is not a web URL; urls takes http(s) pages — a title goes to `harvester_search_literature` first."
		}
	case fieldFiles:
		switch {
		case workNoun(source) != "":
			return "this is " + workNoun(source) + "; put it in publications."
		case isWebURL(source):
			return "this is a URL; put it in urls."
		}
	case fieldPublications:
		switch {
		case workNoun(source) != "", isWebURL(source):
			return ""
		case isLocalInput(source):
			return "this is a local path; " + filesField + "."
		case strings.ContainsAny(strings.TrimSpace(source), " \t"):
			return "this looks like a title; find it with `harvester_search_literature`, then put its handle in publications."
		}
	}
	return ""
}
