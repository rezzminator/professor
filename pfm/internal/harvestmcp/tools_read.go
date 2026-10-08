package harvestmcp

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
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

	// The read tool's three source fields, in the order its input and its
	// answer keep.
	fieldURLs         = "urls"
	fieldFiles        = "files"
	fieldPublications = "publications"

	readDescriptionTail = `1–50 items, at most 20 publications. A title goes to harvester_search_literature first; a file wanted as bytes to harvester_download_file. Returns one text block per item, input order: ` +
		"`=== [n/N] {item}`" + `, `
	readDescriptionEnd = `a partial: or truncated: line when incomplete, then the content. A failed item is its header and an error: line; all failed = an error result.`

	readDescription = `Reads pages, files and papers — web pages, local documents and scholarly works, as Markdown. ` +
		`Call harvester_read{urls:["https://…"], files:["/path/to/file.pdf"], publications:["10.1038/nature14539"]}: ` +
		readDescriptionTail + `the artifact's absolute path, ` + readDescriptionEnd
	readRemoteDescription = `Reads pages and papers — web pages and scholarly works, as Markdown. ` +
		`Call harvester_read{urls:["https://…"], publications:["10.1038/nature14539"]}; this server reads no local files: ` +
		readDescriptionTail + `no path line, ` + readDescriptionEnd
)

// ReadInput is read's input: every source in the field that names its kind.
type ReadInput struct {
	URLs           []string          `json:"urls,omitempty" jsonschema:"Web page URLs (http or https), each read as a page; a PDF or document URL is parsed. An identifier goes in publications, a local path in files."`
	Files          []string          `json:"files,omitempty" jsonschema:"Local document paths (or file:// URLs) on this machine, each parsed to Markdown. A web URL goes in urls."`
	Publications   []string          `json:"publications,omitempty" jsonschema:"At most 20 works: a DOI, arXiv id, PMID, PMCID, ISBN, a harvester_search_literature handle, or a paper or book landing URL, each read as its full text."`
	Refresh        bool              `json:"refresh,omitempty" jsonschema:"Bypass the cache: read every item again and overwrite its cached artifact."`
	IncludeContent *bool             `json:"include_content,omitempty" jsonschema:"Default true. false: read and cache the full content, answering a size line in place of the body."`
	OCRLanguage    string            `json:"ocr_language,omitempty" jsonschema:"Optional script for OCR of a scanned document: latin, zh, ja, ar, ru or he. By default the document's text layer, /Lang or metadata names it, else Latin; set it when a scan's gaps say it was read in Latin. Always a fresh read."`
	Headers        map[string]string `json:"headers,omitempty" jsonschema:"Optional request headers (name → value) sent only to a url's own origin and a publication's landing origin, never with files; reader services, archives and resolver APIs never receive them. At most 32 headers, 8 KiB; no Host, Content-Length, Transfer-Encoding, Connection, Upgrade, TE, Trailer, Keep-Alive or Proxy-*. A caller header overrides the default of its name."`
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

// readSchema is read's input schema. Locally `files` names every format it
// parses (harvest.ReadableFormats). The remote gateway's schema has no
// `files` and stays open to unlisted fields, so a remote call that sends
// `files` reaches read, which refuses it by name.
func readSchema(remote bool) *jsonschema.Schema {
	schema, err := jsonschema.For[ReadInput](nil)
	if err != nil {
		panic(fmt.Sprintf("harvestmcp: infer the read input schema: %v", err))
	}
	if remote {
		delete(schema.Properties, fieldFiles)
		schema.AdditionalProperties = nil
		return schema
	}
	files := schema.Properties[fieldFiles]
	files.Description += " Formats: " + harvest.ReadableFormats + "."
	return schema
}

func (service *Service) read(
	ctx context.Context, _ *mcp.CallToolRequest, input ReadInput,
) (*mcp.CallToolResult, any, error) {
	if service.runtime.Remote && len(input.Files) > 0 {
		return readError(errors.New("files is not available on the remote server: it cannot read a file on " +
			"your machine; send a web URL in urls or an identifier in publications, or read the file with a local harvester")), nil, nil
	}
	total := len(input.URLs) + len(input.Files) + len(input.Publications)
	switch {
	case total == 0:
		return readError(errors.New("read needs at least one item in urls, files or publications")), nil, nil
	case total > maxReadItems:
		return readError(fmt.Errorf(
			"read takes at most %d items in total across urls, files and publications; this call sent %d",
			maxReadItems, total)), nil, nil
	case len(input.Publications) > maxPublications:
		return readError(fmt.Errorf("publications takes at most %d items; this call sent %d",
			maxPublications, len(input.Publications))), nil, nil
	}
	ocrLang, err := harvest.ParseOCRLang(input.OCRLanguage)
	if err != nil {
		return readError(err), nil, nil
	}
	headers, err := harvest.ParseCallerHeaders(input.Headers)
	if err != nil {
		return readError(err), nil, nil
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
	}), nil, nil
}

// readMany fans every job out at once (8 at a time, per-item isolation in
// fetchOne) and answers one text: a block per item in input order (urls,
// files, publications), each headed by the source as the caller passed it.
// Every item failed: the call is an error, so a failed batch never reads as a
// result; one item that read keeps the batch a result.
func (service *Service) readMany(ctx context.Context, jobs []readJob, request readRequest) *mcp.CallToolResult {
	results := make([]harvest.Result, len(jobs))
	misplacedBy := make([]string, len(jobs)) // a misplaced item fails alone, naming the field it belongs in
	var wait sync.WaitGroup
	semaphore := make(chan struct{}, 8)
	for index, job := range jobs {
		if misplacedBy[index] = misplaced(job.field, job.source, service.runtime.Remote); misplacedBy[index] != "" {
			continue
		}
		wait.Add(1)
		go service.fetchOne(ctx, semaphore, &wait, index, job, request, results)
	}
	wait.Wait()
	view := service.readView(request.options.SizeOnly)
	blocks := make([]string, len(jobs))
	isError := true
	for index, job := range jobs {
		block, failed := RenderReadItem(index+1, len(jobs), job.source, results[index], view)
		if misplacedBy[index] != "" {
			block, failed = failedItem(index+1, len(jobs), job.source, misplacedBy[index]), true
		}
		blocks[index] = block
		isError = isError && failed
	}
	return textResult(strings.Join(blocks, "\n\n"), isError)
}

// workJob is the job a publication is read as: an arxiv.org abs or pdf URL
// there names its work, so it reads as that arXiv id (a URL in urls stays the
// page). The answer still names the source as the caller passed it.
func workJob(job readJob) readJob {
	if job.field == fieldPublications {
		if id := harvest.ArXivPageID(job.source); id != "" {
			return readJob{field: job.field, source: "arXiv:" + id}
		}
	}
	return job
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
