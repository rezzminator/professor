package harvestmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	result := callRaw(t, session, name, args)
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

func callRaw(t *testing.T, session *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return result
}

// allText joins every Content text of a result: read's group headings and
// its items.
func allText(result *mcp.CallToolResult) string {
	var texts []string
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			texts = append(texts, text.Text)
		}
	}
	return strings.Join(texts, "\n\n")
}

// readText calls read, decodes its structuredContent into out and returns
// its whole Content text.
func readText(t *testing.T, session *mcp.ClientSession, args map[string]any, out *ReadOutput) string {
	t.Helper()
	result := callRaw(t, session, toolRead, args)
	callStructuredFrom(t, result, out)
	return allText(result)
}

func callStructuredFrom(t *testing.T, result *mcp.CallToolResult, out *ReadOutput) {
	t.Helper()
	if result.StructuredContent == nil {
		t.Fatalf("read returned no structuredContent: %s", allText(result))
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("read structuredContent %s: %v", raw, err)
	}
}

// seedPage stores source as a fresh cached page, so read answers it from the
// cache with no network.
func seedPage(t *testing.T, cacheDir, source, title string) {
	t.Helper()
	path := filepath.Join(cacheDir, harvest.CacheKey(source, "html"))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	raw := "---\nurl: " + source + "\nfetched_at: " + time.Now().UTC().Format(time.RFC3339) +
		"\nsource: harvester\nmethod: direct\n---\n\n# " + title + "\n\n" + strings.Repeat("Words about "+title+". ", 40)
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestToolsListEveryToolWithAnOutputSchema: four tools with a search
// backend, three without, and every tool advertises the output schema its
// struct derives; read's input schema lists `files` only on a local server.
func TestToolsListEveryToolWithAnOutputSchema(t *testing.T) {
	for _, test := range []struct {
		name    string
		runtime Runtime
		want    []string
		files   bool
	}{
		{
			"local with search",
			Runtime{SearXNGURL: "http://searxng.example.test"},
			[]string{toolDownloadFile, toolRead, toolSearchLiterature, toolSearchWeb},
			true,
		},
		{"local without search", Runtime{}, []string{toolDownloadFile, toolRead, toolSearchLiterature}, true},
		{"remote", Runtime{Remote: true}, []string{toolDownloadFile, toolRead, toolSearchLiterature}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			session := connectHarvesterInProcess(t, newTestService(t, test.runtime))
			tools, err := session.ListTools(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, tool := range tools.Tools {
				names = append(names, tool.Name)
				if tool.OutputSchema == nil {
					t.Errorf("%s has no outputSchema", tool.Name)
				}
				if tool.Name != toolRead {
					continue
				}
				raw, err := json.Marshal(tool.InputSchema)
				if err != nil {
					t.Fatal(err)
				}
				if got := strings.Contains(string(raw), `"files"`); got != test.files {
					t.Fatalf("read input schema lists files = %v, want %v: %s", got, test.files, raw)
				}
				if !strings.Contains(string(raw), `"urls"`) || !strings.Contains(string(raw), `"publications"`) {
					t.Fatalf("read input schema lacks urls or publications: %s", raw)
				}
			}
			if fmt.Sprint(names) != fmt.Sprint(test.want) {
				t.Fatalf("tools = %v, want %v", names, test.want)
			}
		})
	}
}

// TestReadRemoteRefusesFilesByName: a remote call that sends `files` fails
// with the named refusal, never a silent drop of the field.
func TestReadRemoteRefusesFilesByName(t *testing.T) {
	session := connectHarvesterInProcess(t, newTestService(t, Runtime{Remote: true}))
	result := callRaw(t, session, toolRead, map[string]any{
		"urls": []string{"https://example.test/a"}, "files": []string{"/tmp/paper.pdf"},
	})
	if text := allText(
		result,
	); !result.IsError ||
		!strings.Contains(text, "files is not available on the remote server") {
		t.Fatalf("remote read with files: isError=%v text=%q, want the named refusal", result.IsError, text)
	}
}

// TestReadGroupsEveryFieldInInputOrder: one read with 2 publications, 4 urls
// and 1 file answers three groups — urls, files, publications — each in the
// input's order, in the structured output and under headings in the text.
func TestReadGroupsEveryFieldInInputOrder(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "cache")
	urls := []string{
		"https://example.test/one", "https://example.test/two",
		"https://example.test/three", "https://example.test/four",
	}
	publications := []string{"https://example.test/paper-a", "https://example.test/paper-b"}
	for index, source := range append(append([]string{}, urls...), publications...) {
		seedPage(t, cacheDir, source, fmt.Sprintf("Page %d", index))
	}
	file := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(file, []byte("# Notes\n\n"+strings.Repeat("A local note. ", 40)), 0o600); err != nil {
		t.Fatal(err)
	}
	session := connectHarvesterInProcess(t, newTestService(t, Runtime{CacheDir: cacheDir}))
	var out ReadOutput
	text := readText(t, session, map[string]any{
		"publications": publications, "urls": urls, "files": []string{file},
	}, &out)
	for _, group := range []struct {
		name  string
		items []ReadItem
		want  []string
	}{{fieldURLs, out.URLs, urls}, {fieldFiles, out.Files, []string{file}}, {fieldPublications, out.Publications, publications}} {
		if len(group.items) != len(group.want) {
			t.Fatalf("%s = %d items, want %d: %+v", group.name, len(group.items), len(group.want), group.items)
		}
		for index, item := range group.items {
			if item.Source != group.want[index] || item.Gaps == nil {
				t.Fatalf("%s[%d] = %+v, want source %q and a gaps list", group.name, index, item, group.want[index])
			}
		}
	}
	for index, item := range out.URLs {
		if item.Error != "" || !item.Cached || item.Via == "" || item.Chars == 0 || item.Tokens == 0 ||
			item.Content == "" || item.Title != fmt.Sprintf("Page %d", index) {
			t.Fatalf("urls[%d] is not a typed cached read: %+v", index, item)
		}
	}
	urlsAt, filesAt, pubsAt := strings.Index(text, "## urls (4)"), strings.Index(text, "## files (1)"),
		strings.Index(text, "## publications (2)")
	if urlsAt < 0 || filesAt < urlsAt || pubsAt < filesAt {
		t.Fatalf("text groups out of order (urls %d, files %d, publications %d):\n%s", urlsAt, filesAt, pubsAt, text)
	}
	if strings.Index(text, urls[3]) > filesAt || strings.Index(text, publications[0]) < pubsAt {
		t.Fatalf("an item rendered outside its group:\n%s", text)
	}
}

// TestReadIncludeContentFalseReturnsNoBody: include_content:false reads and
// caches the item but returns no content, only its size.
func TestReadIncludeContentFalseReturnsNoBody(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "cache")
	seedPage(t, cacheDir, "https://example.test/sized", "Sized")
	session := connectHarvesterInProcess(t, newTestService(t, Runtime{CacheDir: cacheDir}))
	var out ReadOutput
	readText(t, session, map[string]any{"urls": []string{"https://example.test/sized"}, "include_content": false}, &out)
	if len(out.URLs) != 1 || out.URLs[0].Error != "" || out.URLs[0].Content != "" || out.URLs[0].Chars == 0 {
		t.Fatalf("include_content false: %+v, want a sized item with no content", out.URLs)
	}
}

// TestReadMisplacedItemFailsAloneNamingTheField: each misplaced item is its
// own named error pointing at the right field, and the rest of the call
// still reads.
func TestReadMisplacedItemFailsAloneNamingTheField(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "cache")
	seedPage(t, cacheDir, "https://example.test/ok", "Fine")
	session := connectHarvesterInProcess(t, newTestService(t, Runtime{CacheDir: cacheDir}))
	var out ReadOutput
	result := callRaw(t, session, toolRead, map[string]any{
		"urls":         []string{"10.1038/nature14539", "/tmp/paper.pdf", "https://example.test/ok"},
		"files":        []string{"https://example.test/page", "arXiv:1706.03762"},
		"publications": []string{"./paper.pdf", "Attention is all you need"},
	})
	callStructuredFrom(t, result, &out)
	if result.IsError {
		t.Fatalf("one item read, yet the call is an error: %s", allText(result))
	}
	for _, check := range []struct {
		item ReadItem
		want string
	}{
		{out.URLs[0], "put it in publications"},
		{out.URLs[1], "put it in files"},
		{out.Files[0], "put it in urls"},
		{out.Files[1], "put it in publications"},
		{out.Publications[0], "put it in files"},
		{out.Publications[1], "`search_literature`"},
	} {
		if !strings.Contains(check.item.Error, check.want) {
			t.Errorf("%s: error %q, want it to name %q", check.item.Source, check.item.Error, check.want)
		}
	}
	if out.URLs[2].Error != "" || out.URLs[2].Content == "" {
		t.Fatalf("the well-placed url did not read: %+v", out.URLs[2])
	}
	landing := "https://www.nature.com/articles/nature14539"
	if misplaced(fieldURLs, landing, false) != "" || misplaced(fieldPublications, landing, false) != "" {
		t.Fatal("a paper landing URL must be valid in both urls and publications")
	}
	if ids := workIDs("doi:10.1038/nature14539"); ids["doi"] != "10.1038/nature14539" {
		t.Fatalf("workIDs(doi) = %v", ids)
	}
}

// TestReadBatchLimitsAreNamedErrors: no item, more than 50 in total and more
// than 20 publications each fail the call with a named error.
func TestReadBatchLimitsAreNamedErrors(t *testing.T) {
	session := connectHarvesterInProcess(t, newTestService(t, Runtime{}))
	many := func(n int, format string) []string {
		out := make([]string, n)
		for index := range out {
			out[index] = fmt.Sprintf(format, index)
		}
		return out
	}
	for _, test := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"empty", map[string]any{}, "read needs at least one item in urls, files or publications"},
		{"51 in total", map[string]any{
			"urls": many(40, "https://example.test/%d"), "publications": many(11, "10.1000/%d"),
		}, "read takes at most 50 items in total across urls, files and publications; this call sent 51"},
		{
			"21 publications",
			map[string]any{"publications": many(21, "10.1000/%d")},
			"publications takes at most 20 items; this call sent 21",
		},
	} {
		result := callRaw(t, session, toolRead, test.args)
		if text := allText(result); !result.IsError || !strings.Contains(text, test.want) {
			t.Errorf("%s: isError=%v text=%q, want %q", test.name, result.IsError, text, test.want)
		}
	}
}

// TestReadAllFailedIsAnError: a call whose every item failed answers isError
// true, so a caller never reads a failed batch as a result; the item's own
// named text rides in the Content.
func TestReadAllFailedIsAnError(t *testing.T) {
	session := connectHarvesterInProcess(t, newTestService(t, Runtime{}))
	result := callRaw(t, session, toolRead, map[string]any{"files": []string{"https://example.test/page"}})
	if !result.IsError || !strings.Contains(allText(result), "put it in urls") {
		t.Fatalf("read with every item failed: isError = %v, content %s", result.IsError, allText(result))
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

// TestReadArXivPublicationsReadTheWork: an arXiv id — prefixed, bare, or as an
// arxiv.org abs/pdf URL — in publications reads the work through its arXiv
// DOI, echoing the caller's source and naming its arxiv id; the same abs URL
// in urls reads the page.
func TestReadArXivPublicationsReadTheWork(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "cache")
	abs := "https://arxiv.org/abs/1706.03762"
	seedPage(t, cacheDir, "10.48550/arXiv.1706.03762", "Attention Paper")
	seedPage(t, cacheDir, abs, "Abs Landing")
	publications := []string{"arXiv:1706.03762", "1706.03762", abs, "https://arxiv.org/pdf/1706.03762"}
	session := connectHarvesterInProcess(t, newTestService(t, Runtime{CacheDir: cacheDir}))
	var out ReadOutput
	text := readText(t, session, map[string]any{"urls": []string{abs}, "publications": publications}, &out)
	if len(out.URLs) != 1 || out.URLs[0].Error != "" || out.URLs[0].Title != "Abs Landing" {
		t.Fatalf("urls = %+v, want the abs page read as a page", out.URLs)
	}
	if len(out.Publications) != len(publications) {
		t.Fatalf("publications = %+v, want %d items", out.Publications, len(publications))
	}
	for index, item := range out.Publications {
		if item.Source != publications[index] || item.Error != "" || item.Title != "Attention Paper" ||
			item.IDs["arxiv"] != "1706.03762" {
			t.Fatalf("publications[%d] = %+v, want the arXiv work read for %q", index, item, publications[index])
		}
		if !strings.Contains(text, "# "+publications[index]+"\n") {
			t.Fatalf("publications[%d] text lost its source header %q:\n%s", index, publications[index], text)
		}
	}
}
