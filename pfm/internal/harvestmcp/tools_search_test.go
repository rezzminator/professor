package harvestmcp

import (
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestSearchWebFailureIsAnErrorNeverAnEmptyList: a dead backend answers an
// error result whose typed output names the failure beside empty results.
func TestSearchWebFailureIsAnErrorNeverAnEmptyList(t *testing.T) {
	session := connectHarvesterInProcess(t, newTestService(t, Runtime{SearXNGURL: "http://127.0.0.1:1/search"}))
	var out SearchOutput
	text := callStructured(t, session, toolSearchWeb, map[string]any{"query": "q"}, &out)
	if out.Error == "" || len(out.Results) != 0 || !strings.Contains(text, "Web search failed") {
		t.Fatalf("search_web(dead backend) = %+v text=%q", out, text)
	}
}

// TestSearchWebTakesLimit: limit bounds the result count; out of range is a
// named error naming limit.
func TestSearchWebTakesLimit(t *testing.T) {
	session := connectHarvesterInProcess(t, newTestService(t, Runtime{SearXNGURL: "http://127.0.0.1:1/search"}))
	result := callRaw(t, session, toolSearchWeb, map[string]any{"query": "q", "limit": 21})
	if text, _ := result.Content[0].(*mcp.TextContent); !result.IsError || text == nil ||
		text.Text != "limit must be between 1 and 20" {
		t.Fatalf("search_web(limit 21) = %+v, want the named limit error", result.Content)
	}
	var out SearchOutput
	callStructured(t, session, toolSearchWeb, map[string]any{"query": "q", "limit": 3}, &out)
	if out.Error == "" {
		t.Fatalf("search_web(limit 3, dead backend) = %+v, want the search failure", out)
	}
}
