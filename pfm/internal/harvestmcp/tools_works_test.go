package harvestmcp

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestSearchLiteratureRefusesAnUnknownType: type is any, paper or book.
func TestSearchLiteratureRefusesAnUnknownType(t *testing.T) {
	session := connectHarvesterInProcess(t, newTestService(t, Runtime{}))
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: toolSearchLiterature, Arguments: map[string]any{"query": "x", "type": "movie"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError ||
		result.Content[0].(*mcp.TextContent).Text != `type must be any, paper or book, got "movie"` {
		t.Fatalf("search_literature(type movie) = %+v, want a named type error", result)
	}
}

// TestSearchLiteratureTypeRoundTrips: type narrows the call through the SDK,
// and each candidate names its own type under the same word.
func TestSearchLiteratureTypeRoundTrips(t *testing.T) {
	service := newTestService(t, Runtime{})
	service.resolver.Client = &http.Client{Transport: findRoundTrip(func(r *http.Request) (int, string) {
		if r.URL.Host == "api.openalex.org" {
			return http.StatusOK, `{"results":[{"doi":"https://doi.org/10.1038/nature14539","display_name":"Deep learning","publication_year":2015}]}`
		}
		return http.StatusOK, `{}`
	})}
	found, err := service.SearchLiterature(context.Background(), FindInput{Query: "Deep learning", Type: "paper"})
	if err != nil || len(found.Candidates) == 0 {
		t.Fatalf("search_literature(type paper) = %+v (err %v), want candidates", found, err)
	}
	for _, candidate := range found.Candidates {
		if candidate.Type == "" {
			t.Fatalf("candidate = %+v, want its type", candidate)
		}
	}
	session := connectHarvesterInProcess(t, service)
	result, text := callText(
		t,
		session,
		toolSearchLiterature,
		map[string]any{"query": "Deep learning", "type": "paper"},
	)
	if result.IsError || !strings.Contains(text, "=== [1/") ||
		!strings.Contains(text, "\n"+found.Candidates[0].Type) {
		t.Fatalf("search_literature(type paper) text = %q, want a block naming its type", text)
	}
}

// TestSearchLiteratureNamesAFailedSource: a source that answered HTTP 429 is named
// in the typed sources and in the text, apart from one that answered with
// nothing, and the other sources' candidates stay.
func TestSearchLiteratureNamesAFailedSource(t *testing.T) {
	service := newTestService(t, Runtime{})
	service.resolver.Client = &http.Client{Transport: findRoundTrip(func(r *http.Request) (int, string) {
		switch r.URL.Host {
		case "api.semanticscholar.org":
			return http.StatusTooManyRequests, `{}`
		case "api.openalex.org":
			return http.StatusOK, `{"results":[{"doi":"https://doi.org/10.1038/nature14539","display_name":"Deep learning","publication_year":2015}]}`
		}
		return http.StatusOK, `{}`
	})}
	output, err := service.SearchLiterature(context.Background(), FindInput{Query: "Deep learning"})
	if err != nil || len(output.Candidates) == 0 {
		t.Fatalf("search_literature = %+v (err %v), want OpenAlex's record", output, err)
	}
	named := map[string]FindSource{}
	for _, source := range output.Sources {
		named[source.Source] = source
	}
	if got := named["Semantic Scholar"]; got.Status != "failed" || !strings.Contains(got.Error, "HTTP 429") {
		t.Fatalf("Semantic Scholar = %+v, want failed with HTTP 429", got)
	}
	if got := named["Crossref"]; got.Status != "answered" {
		t.Fatalf("Crossref = %+v, want answered with no result", got)
	}
	session := connectHarvesterInProcess(t, service)
	result, text := callText(t, session, toolSearchLiterature, map[string]any{"query": "Deep learning"})
	if result.IsError || !strings.Contains(text, "Semantic Scholar failed (0): ") ||
		!strings.Contains(text, "HTTP 429") ||
		!strings.Contains(text, "Crossref answered (0)") {
		t.Fatalf("text does not name every source's status:\n%s", text)
	}
}

type findRoundTrip func(*http.Request) (int, string)

func (answer findRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) {
	status, body := answer(r)
	return &http.Response{
		StatusCode: status, Status: http.StatusText(status), Request: r, ProtoMajor: 1, ProtoMinor: 1,
		Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)),
	}, nil
}

// TestSearchLiteratureNamesAHandleFailureClass: when the discovered works cannot be
// given retrieval handles, the caller reads what failed and whether a retry
// helps, never a bare "retry later", and no machine path.
func TestSearchLiteratureNamesAHandleFailureClass(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "cache")
	if err := os.WriteFile(cache, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := newTestService(t, Runtime{CacheDir: cache})
	service.resolver.Client = &http.Client{Transport: findRoundTrip(func(r *http.Request) (int, string) {
		if r.URL.Host == "api.openalex.org" {
			return http.StatusOK, `{"results":[{"display_name":"Deep learning","publication_year":2015,` +
				`"open_access":{"oa_url":"https://hal.science/hal-00000001"}}]}`
		}
		return http.StatusOK, `{}`
	})}
	session := connectHarvesterInProcess(t, service)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: toolSearchLiterature, Arguments: map[string]any{"query": "Deep learning"},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].(*mcp.TextContent).Text
	if !result.IsError {
		t.Fatalf("search_literature with an unwritable cache = %s, want a tool error", text)
	}
	for _, want := range []string{"cache", "not a directory", "retrying will not help"} {
		if !strings.Contains(text, want) {
			t.Errorf("error text lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, cache) || strings.Contains(text, "retry later") {
		t.Errorf("error text leaks the cache path or hides behind retry later:\n%s", text)
	}
}
