package harvestmcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestCallerHeadersInTheInputSchemas: readPage, download and readWork list
// `headers`; findWorks and webSearch, whose requests go to metadata and search
// APIs, do not.
func TestCallerHeadersInTheInputSchemas(t *testing.T) {
	session := connectHarvesterInProcess(t, newTestService(t, Runtime{SearXNGURL: "http://searxng.example.test"}))
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		toolReadPage: true, toolDownload: true, toolReadWork: true,
		toolFindWorks: false, toolWebSearch: false, toolParseLocal: false,
	}
	listed := 0
	for _, tool := range tools.Tools {
		expected, known := want[tool.Name]
		if !known {
			continue
		}
		listed++
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(string(raw), `"headers"`); got != expected {
			t.Errorf("%s input schema lists headers = %v, want %v: %s", tool.Name, got, expected, raw)
		}
	}
	if listed != len(want) {
		t.Fatalf("checked %d of the %d tools", listed, len(want))
	}
}

// TestCallerHeadersRefusedByTheTools: a refused header set fails the call
// with a named error that never carries the value.
func TestCallerHeadersRefusedByTheTools(t *testing.T) {
	const value = "sentinel-7f3a9c"
	session := connectHarvesterInProcess(t, newTestService(t, Runtime{}))
	for tool, key := range map[string]string{toolReadPage: "sources", toolDownload: "sources", toolReadWork: "works"} {
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
			Name: tool,
			Arguments: map[string]any{
				key:       []string{"https://example.test/a"},
				"headers": map[string]string{"Host": value},
			},
		})
		text := ""
		if err != nil {
			text = err.Error()
		} else if result.IsError && len(result.Content) > 0 {
			if content, ok := result.Content[0].(*mcp.TextContent); ok {
				text = content.Text
			}
		}
		if !strings.Contains(text, "caller header refused") || strings.Contains(text, value) {
			t.Fatalf("%s with a Host header: %q, want the named refusal without the value", tool, text)
		}
	}
}
