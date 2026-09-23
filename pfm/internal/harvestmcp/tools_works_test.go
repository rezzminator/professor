package harvestmcp

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestFindWorksRefusesAnUnknownKind: kind is any, paper or book.
func TestFindWorksRefusesAnUnknownKind(t *testing.T) {
	session := connectHarvesterInProcess(t, newTestService(t, Runtime{}))
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: toolFindWorks, Arguments: map[string]any{"query": "x", "kind": "movie"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "kind must be") {
		t.Fatalf("findWorks(kind movie) = %+v, want a named kind error", result)
	}
}
