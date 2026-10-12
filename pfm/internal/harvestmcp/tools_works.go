package harvestmcp

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/harvest"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

const (
	maxFindResults              = 25
	workTypeAny                 = "any"
	workTypePaper               = "paper"
	workTypeBook                = "book"
	searchLiteratureDescription = `Finds papers and books — by TITLE or bibliographic query: "find the paper about X", "is there a PDF of <title>". Call harvester_search_literature{query:"Attention Is All You Need", type:"paper"}. Returns a ` + "`=== [n/N] {title}`" + ` block per ranked candidate — handle, type · authors · year, ids, open access; pass the handle unchanged to ` + "`harvester_read`" + ` in publications — then sources:, each one's status. "No candidate works found" with every source answered = nothing matched; a failed source is named, its records missing; an error result = every source failed, retry or read an exact identifier.`
)

// FindInput is search_literature's input.
type FindInput struct {
	Query string `json:"query" jsonschema:"A paper or book TITLE, or a free-text bibliographic query."`
	Limit int    `json:"limit,omitempty" jsonschema:"Maximum number of candidate works to return, default 8 and maximum 25."`
	Type  string `json:"type,omitempty" jsonschema:"any (default), paper, or book."`
}

// WorkCandidate is one ranked search_literature candidate; Handle goes to read in publications.
type WorkCandidate struct {
	Handle     string            `json:"handle"`
	Title      string            `json:"title,omitempty"`
	Authors    string            `json:"authors,omitempty"`
	Year       int               `json:"year,omitempty"`
	Type       string            `json:"type,omitempty"`
	IDs        map[string]string `json:"ids,omitempty"`
	OpenAccess bool              `json:"open_access"`
}

// FindSource is one discovery source's status in a search_literature call: a source
// that failed is named apart from one that answered with nothing.
type FindSource struct {
	Source  string `json:"source"`
	Status  string `json:"status" jsonschema:"answered, partial (some of its requests failed), failed, or timed_out (still running at the harvester_search_literature deadline, cancelled)."`
	Results int    `json:"results"`
	Error   string `json:"error,omitempty"`
}

// FindOutput is what one search_literature call found: the candidates and
// every source's status. The tool answers it as text (RenderFind); pfm
// harvest search --json prints it.
type FindOutput struct {
	Candidates []WorkCandidate `json:"candidates"`
	Sources    []FindSource    `json:"sources"`
}

func (service *Service) searchLiteratureTool(
	ctx context.Context, _ *mcp.CallToolRequest, input FindInput,
) (*mcp.CallToolResult, any, error) {
	found, err := service.SearchLiterature(ctx, input)
	if err != nil {
		return nil, nil, err
	}
	return textResult(RenderFind(input.Query, found), false), nil, nil
}

// SearchLiterature runs one search_literature query: the tool's and pfm
// harvest search's one search. An error is a refused input, every source
// failing, or the candidates' handles not being storable.
func (service *Service) SearchLiterature(ctx context.Context, input FindInput) (FindOutput, error) {
	if strings.TrimSpace(input.Query) == "" {
		return FindOutput{}, errors.New("query must not be empty")
	}
	if input.Limit == 0 {
		input.Limit = 8
	}
	if input.Limit < 1 || input.Limit > maxFindResults {
		return FindOutput{}, fmt.Errorf("limit must be between 1 and %d", maxFindResults)
	}
	workType := strings.ToLower(strings.TrimSpace(input.Type))
	switch workType {
	case "", workTypeAny, workTypePaper, workTypeBook:
	default:
		return FindOutput{}, fmt.Errorf(
			"type must be %s, %s or %s, got %q",
			workTypeAny,
			workTypePaper,
			workTypeBook,
			input.Type,
		)
	}
	log := obs.Logger(obs.Component(ctx, "mcp"))
	found, err := service.resolver.FindWorksReportFor(ctx, input.Query, input.Limit, workType)
	if err != nil {
		log.Warn("harvester.search_literature.failed", obs.FieldErr, err.Error())
		return FindOutput{}, fmt.Errorf(
			"work discovery failed: every source failed — %s; retry later or read an exact identifier with `harvester_read` (publications)",
			harvest.FailedText(found.Failed()),
		)
	}
	sources := make([]FindSource, 0, len(found.Sources))
	for _, source := range found.Sources {
		if source.Status != harvest.SourceAnswered {
			log.Warn(
				"harvester.search_literature.source",
				"source",
				source.Name,
				"status",
				source.Status,
				obs.FieldErr,
				source.Error,
			)
		}
		sources = append(sources, FindSource{
			Source: source.Name, Status: source.Status, Results: source.Results, Error: source.Error,
		})
	}
	candidates, err := service.harvester.PublicCandidates(found.Candidates)
	if err != nil {
		log.Warn("harvester.search_literature.export", obs.FieldErr, err.Error())
		return FindOutput{}, handleFailure(err)
	}
	out := make([]WorkCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		isBook := strings.Contains(strings.ToLower(candidate.Kind), workTypeBook)
		if (workType == workTypeBook && !isBook) || (workType == workTypePaper && isBook) {
			continue
		}
		out = append(out, WorkCandidate{
			Handle: candidate.URL, Title: candidate.Title, Authors: candidate.Authors, Year: candidate.Year,
			Type: candidate.Kind, IDs: workIDs(candidate.URL), OpenAccess: candidate.Free != "",
		})
	}
	return FindOutput{Candidates: out, Sources: sources}, nil
}

// handleFailure names why the discovered works got no retrieval handles: the
// harvester's cache refused the write (its operation and cause, never its
// path) or a handle rule refused a work. Neither is a network failure, so the
// same call retried answers the same until the cause is fixed.
func handleFailure(err error) error {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return fmt.Errorf(
			"could not prepare the discovered works for retrieval: the harvester's cache refused to store "+
				"their handles (%s: %v); retrying will not help until the cache directory is writable",
			pathErr.Op,
			pathErr.Err,
		)
	}
	return fmt.Errorf(
		"could not prepare the discovered works for retrieval: %v; retrying will not help — "+
			"read a work's exact identifier with `harvester_read` (publications)",
		err,
	)
}
