package harvestmcp

import (
	"strings"
	"testing"
)

// TestWebSearchFailureIsAnErrorNeverAnEmptyList: a dead backend answers an
// error result whose typed output names the failure beside empty results.
func TestWebSearchFailureIsAnErrorNeverAnEmptyList(t *testing.T) {
	session := connectHarvesterInProcess(t, newTestService(t, Runtime{SearXNGURL: "http://127.0.0.1:1/search"}))
	var out SearchOutput
	text := callStructured(t, session, toolWebSearch, map[string]any{"query": "q"}, &out)
	if out.Error == "" || len(out.Results) != 0 || !strings.Contains(text, "Web search failed") {
		t.Fatalf("webSearch(dead backend) = %+v text=%q", out, text)
	}
}
