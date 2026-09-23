package harvestmcp

import (
	"context"
	"errors"
	"fmt"
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
	findWorksDescription = `Finds scholarly papers and books by TITLE or bibliographic query — "find the paper about X", "is there a PDF of <title>". No download. Call findWorks{query:"Attention Is All You Need"}; kind:"paper" or "book" narrows it. Returns ranked candidates (title, authors, year, kind, identifiers, open access) each with a handle — pass that value unchanged to readWork. Empty candidates = nothing matched (give the exact title); a tool error = discovery itself failed, retry or read an exact identifier with readWork.`
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

// FindOutput is findWorks' typed output.
type FindOutput struct {
	Candidates []WorkCandidate `json:"candidates"`
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
	candidates, err := service.resolver.FindWorks(ctx, input.Query, input.Limit)
	if err != nil {
		log.Warn("harvester.findWorks.failed", obs.FieldErr, err.Error())
		return nil, FindOutput{}, errors.New(
			"work discovery failed; retry later or read an exact identifier with readWork",
		)
	}
	candidates, err = service.harvester.PublicCandidates(candidates)
	if err != nil {
		log.Warn("harvester.findWorks.export", obs.FieldErr, err.Error())
		return nil, FindOutput{}, errors.New("could not prepare the discovered works for retrieval; retry later")
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
		Content: []mcp.Content{&mcp.TextContent{Text: renderFind(input.Query, kept)}},
	}, FindOutput{Candidates: out}, nil
}
