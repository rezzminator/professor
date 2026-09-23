package harvestmcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/harvest"
)

func newTestService(t *testing.T, runtime Runtime) *Service {
	t.Helper()
	if runtime.Home == "" {
		runtime.Home = t.TempDir()
	}
	if runtime.CacheDir == "" {
		runtime.CacheDir = filepath.Join(t.TempDir(), "cache")
	}
	service, err := NewConfiguredHarvester("test", runtime)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	return service
}

// callStructured calls a tool over the in-memory SDK transport and returns
// its structuredContent decoded into out, plus the first Content text.
func callStructured(t *testing.T, session *mcp.ClientSession, name string, args, out any) string {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if result.StructuredContent == nil {
		t.Fatalf("%s returned no structuredContent: %+v", name, result)
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("%s structuredContent %s: %v", name, raw, err)
	}
	if len(result.Content) == 0 {
		t.Fatalf("%s returned no Content text", name)
	}
	text, _ := result.Content[0].(*mcp.TextContent)
	if text == nil {
		t.Fatalf("%s Content[0] = %T, want text", name, result.Content[0])
	}
	return text.Text
}

// TestToolsListEveryToolWithAnOutputSchema: six tools with a search backend,
// five without, parseLocalDocuments never on the remote server, and every
// tool advertises the output schema its struct derives.
func TestToolsListEveryToolWithAnOutputSchema(t *testing.T) {
	for _, test := range []struct {
		name    string
		runtime Runtime
		want    int
		local   bool
	}{
		{"local with search", Runtime{SearXNGURL: "http://searxng.example.test"}, 6, true},
		{"local without search", Runtime{}, 5, true},
		{"remote", Runtime{Remote: true}, 4, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			session := connectHarvesterInProcess(t, newTestService(t, test.runtime))
			tools, err := session.ListTools(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(tools.Tools) != test.want {
				t.Fatalf("tools = %d, want %d", len(tools.Tools), test.want)
			}
			hasLocal := false
			for _, tool := range tools.Tools {
				hasLocal = hasLocal || tool.Name == toolParseLocal
				if tool.OutputSchema == nil {
					t.Errorf("%s has no outputSchema", tool.Name)
				}
			}
			if hasLocal != test.local {
				t.Fatalf("parseLocalDocuments listed = %v, want %v", hasLocal, test.local)
			}
		})
	}
}

// TestReadToolsNameTheRightTool: a DOI to readPage names readWork, a local
// path to readPage names parseLocalDocuments, a URL to parseLocalDocuments
// names readPage — through the SDK, so the typed output validates.
func TestReadToolsNameTheRightTool(t *testing.T) {
	session := connectHarvesterInProcess(t, newTestService(t, Runtime{}))
	for _, test := range []struct{ tool, key, source, want string }{
		{toolReadPage, "sources", "10.1038/nature14539", "`readWork`"},
		{toolReadPage, "sources", "/tmp/paper.pdf", "`parseLocalDocuments`"},
		{toolParseLocal, "paths", "https://example.test/page", "`readPage`"},
		{toolReadWork, "works", "./paper.pdf", "`parseLocalDocuments`"},
	} {
		var out PagesOutput
		text := callStructured(t, session, test.tool, map[string]any{test.key: []string{test.source}}, &out)
		if len(out.Items) != 1 || !strings.Contains(out.Items[0].Error, test.want) {
			t.Errorf("%s(%s) items = %+v, want an error naming %s", test.tool, test.source, out.Items, test.want)
		}
		if !strings.Contains(text, test.want) {
			t.Errorf("%s(%s) text = %q, want it to name %s", test.tool, test.source, text, test.want)
		}
	}
	if pageMisroute("https://www.nature.com/articles/nature14539") != "" ||
		workMisroute("https://www.nature.com/articles/nature14539") != "" {
		t.Fatal("a paper landing URL must be valid for both readPage and readWork")
	}
	if ids := workIDs("doi:10.1038/nature14539"); ids["doi"] != "10.1038/nature14539" {
		t.Fatalf("workIDs(doi) = %v", ids)
	}
}

// TestReadToolAllFailedIsAnError: a call whose every item failed answers
// isError true, so a caller never reads a failed batch as a result; the
// item's own named text rides in the Content.
func TestReadToolAllFailedIsAnError(t *testing.T) {
	session := connectHarvesterInProcess(t, newTestService(t, Runtime{}))
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: toolParseLocal, Arguments: map[string]any{"paths": []string{"https://example.test/page"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("parseLocalDocuments with every item failed: isError = false, content %+v", result.Content)
	}
}

// TestDescribeFetchKeepsAPublishedFailuresText: the render's second pass over
// a published failure keeps its text — a converter's named failure is never
// re-read as "The title is ambiguous".
func TestDescribeFetchKeepsAPublishedFailuresText(t *testing.T) {
	service := newTestService(t, Runtime{})
	source := "/tmp/demo/broken-feed.xml"
	published := harvest.PublicFailure(source, harvest.Result{
		Source: source, Kind: "feed",
		Error: "harvestpy conversion failed (ValueError): ValueError: the feed parsed to no title and no items: " +
			"a broken or empty feed; a feedparser fallback on a broken feed is unmeasured (stderr: )",
	})
	text := service.describeFetch(source, published, false)
	if strings.Contains(text, "ambiguous") || !strings.Contains(text, "a broken or empty feed") {
		t.Fatalf("describeFetch = %q, want the converter's named failure", text)
	}
}
