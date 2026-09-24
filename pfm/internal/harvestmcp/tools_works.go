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
	maxFindResults       = 25
	workKindAny          = "any"
	workKindPaper        = "paper"
	workKindBook         = "book"
	findWorksDescription = `Finds scholarly papers and books by TITLE or bibliographic query — "find the paper about X", "is there a PDF of <title>". No download. Call findWorks{query:"Attention Is All You Need"}; kind:"paper" or "book" narrows it. Returns ranked candidates (title, authors, year, kind, identifiers, open access) each with a handle — pass that value unchanged to readWork. ` + "`sources`" + ` names each discovery source's status (answered, partial, failed, timed_out = still running at the 20 s deadline, or 2 s after the sources for the kind finished, and cancelled): empty candidates with every source answered = nothing matched (give the exact title); a failed source is named, never read as an empty answer; a tool error = every source failed, retry later or read an exact identifier with readWork.`
)

// FindInput is findWorks' input.
type FindInput struct {
	Query string `json:"query" jsonschema:"A paper or book TITLE, or a free-text bibliographic query."`
	Limit int    `json:"limit,omitempty" jsonschema:"Maximum number of candidate works to return, default 8 and maximum 25."`
	Kind  string `json:"kind,omitempty" jsonschema:"any (default), paper, or book."`
}

// WorkCandidate is one ranked findWorks candidate; Handle goes to readWork.
type WorkCandidate struct {
	Handle     string            `json:"handle"`
	Title      string            `json:"title,omitempty"`
	Authors    string            `json:"authors,omitempty"`
	Year       int               `json:"year,omitempty"`
	Kind       string            `json:"kind,omitempty"`
	IDs        map[string]string `json:"ids,omitempty"`
	OpenAccess bool              `json:"open_access"`
}

// FindSource is one discovery source's status in a findWorks call: a source
// that failed is named apart from one that answered with nothing.
type FindSource struct {
	Source  string `json:"source"`
	Status  string `json:"status" jsonschema:"answered, partial (some of its requests failed), failed, or timed_out (still running at the findWorks deadline, cancelled)."`
	Results int    `json:"results"`
	Error   string `json:"error,omitempty"`
}

// FindOutput is findWorks' typed output.
type FindOutput struct {
	Candidates []WorkCandidate `json:"candidates"`
	Sources    []FindSource    `json:"sources"`
}

func (service *Service) findWorks(
	ctx context.Context, _ *mcp.CallToolRequest, input FindInput,
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
	kind := strings.ToLower(strings.TrimSpace(input.Kind))
	switch kind {
	case "", workKindAny, workKindPaper, workKindBook:
	default:
		return nil, FindOutput{}, fmt.Errorf(
			"kind must be %s, %s or %s, got %q",
			workKindAny,
			workKindPaper,
			workKindBook,
			input.Kind,
		)
	}
	log := obs.Logger(obs.Component(ctx, "mcp"))
	found, err := service.resolver.FindWorksReportFor(ctx, input.Query, input.Limit, kind)
	if err != nil {
		log.Warn("harvester.findWorks.failed", obs.FieldErr, err.Error())
		return nil, FindOutput{}, fmt.Errorf(
			"work discovery failed: every source failed — %s; retry later or read an exact identifier with readWork",
			harvest.FailedText(found.Failed()),
		)
	}
	sources := make([]FindSource, 0, len(found.Sources))
	for _, source := range found.Sources {
		if source.Status != harvest.SourceAnswered {
			log.Warn(
				"harvester.findWorks.source",
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
		log.Warn("harvester.findWorks.export", obs.FieldErr, err.Error())
		return nil, FindOutput{}, handleFailure(err)
	}
	kept := make([]harvest.Candidate, 0, len(candidates))
	out := make([]WorkCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		isBook := strings.Contains(strings.ToLower(candidate.Kind), workKindBook)
		if (kind == workKindBook && !isBook) || (kind == workKindPaper && isBook) {
			continue
		}
		kept = append(kept, candidate)
		out = append(out, WorkCandidate{
			Handle: candidate.URL, Title: candidate.Title, Authors: candidate.Authors, Year: candidate.Year,
			Kind: candidate.Kind, IDs: workIDs(candidate.URL), OpenAccess: candidate.Free != "",
		})
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: renderFind(input.Query, kept, found.Failed())}},
	}, FindOutput{Candidates: out, Sources: sources}, nil
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
			"read a work's exact identifier with readWork",
		err,
	)
}
