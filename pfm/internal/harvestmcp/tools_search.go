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
	maxSearchResults     = 20
	webSearchDescription = `Searches the web — "search for X", "find pages about X" — ranked titles, URLs, snippets and the engine that found each, never the page itself. Call webSearch{query:"…"}; webSearch{query:"…", lang:"ja"} reaches a non-English literature. Pass a result URL to readPage for the page. Empty results = nothing matched (broaden the query); an error result = the search backend failed, retry later. For a paper or book by title use findWorks.`
)

// SearchInput is webSearch's input.
type SearchInput struct {
	Query   string `json:"query" jsonschema:"The web search query."`
	Count   int    `json:"count,omitempty" jsonschema:"Maximum number of results to return, default 8 and maximum 20."`
	Lang    string `json:"lang,omitempty" jsonschema:"Optional language/locale to bias the search (e.g. zh, ja, pt-BR). Set it to reach non-English literature."`
	Engines string `json:"engines,omitempty" jsonschema:"Optional comma-separated SearXNG engines to restrict to (e.g. google,brave or naver,yahoo). Omit for the default aggregated set."`
}

// WebResult is one webSearch result.
type WebResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
	Engine  string `json:"engine,omitempty"`
}

// SearchOutput is webSearch's typed output; Error names a failed search, so
// an empty Results list never stands for a failure.
type SearchOutput struct {
	Results []WebResult `json:"results"`
	Error   string      `json:"error,omitempty"`
}

func (service *Service) webSearch(
	ctx context.Context, _ *mcp.CallToolRequest, input SearchInput,
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
	results, backend, err := harvest.Search(ctx, input.Query, harvest.SearchOptions{
		SearXNGURL:    service.runtime.SearXNGURL,
		BraveAPIKey:   service.runtime.BraveAPIKey,
		DisableSearch: service.runtime.DisableSearch,
		Lang:          input.Lang,
		Engines:       input.Engines,
		Count:         input.Count,
	})
	if err != nil {
		// err.Error() is the FULL diagnostic (search.go) — may carry a backend
		// URL with userinfo — so it goes through the scrubbed activity log,
		// never raw to stderr; renderSearchFailure is the one safe rendering
		// both stderr and the tool result share.
		obs.Logger(obs.Component(ctx, "mcp")).Warn("harvester.search.failed", obs.FieldErr, err.Error())
		safe := renderSearchFailure(err)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: safe}},
			IsError: true,
		}, SearchOutput{Results: []WebResult{}, Error: safe}, nil
	}
	out := make([]WebResult, 0, len(results))
	for _, result := range results {
		out = append(out, WebResult{
			Title: result.Title, URL: result.URL, Snippet: result.Snippet, Engine: valueOr(result.Engine, backend),
		})
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: renderSearch(input.Query, results, "")}},
	}, SearchOutput{Results: out}, nil
}
