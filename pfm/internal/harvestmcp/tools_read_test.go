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

// callText calls a tool over the in-memory SDK transport and returns its
// result with every Content text joined; a tool answering structuredContent
// fails the test, since every tool answers text alone.
func callText(t *testing.T, session *mcp.ClientSession, name string, args any) (*mcp.CallToolResult, string) {
	t.Helper()
	result := callRaw(t, session, name, args)
	if result.StructuredContent != nil {
		t.Fatalf("%s answered structuredContent: %v", name, result.StructuredContent)
	}
	return result, allText(result)
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

// TestToolsListNoToolHasAnOutputSchema: four tools with a search backend,
// three without, and none advertises an output schema, since every tool
// answers text alone; read's input schema lists `files` only on a local
// server.
func TestToolsListNoToolHasAnOutputSchema(t *testing.T) {
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
				if tool.OutputSchema != nil {
					t.Errorf("%s advertises an outputSchema", tool.Name)
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
				if got := strings.Contains(string(raw), harvest.ReadableFormats); got != test.files {
					t.Fatalf("read input schema names the file formats = %v, want %v: %s", got, test.files, raw)
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

// TestReadNumbersEveryItemInInputOrder: one read with 2 publications, 4 urls
// and 1 file answers one block per item — urls, then files, then
// publications, each in the input's order — numbered over the whole call.
func TestReadNumbersEveryItemInInputOrder(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "cache")
	urls := []string{
		"https://example.test/one", "https://example.test/two",
		"https://example.test/three", "https://example.test/four",
	}
	publications := []string{"https://example.test/paper-a", "https://example.test/paper-b"}
	sources := append(append([]string{}, urls...), publications...)
	for index, source := range sources {
		seedPage(t, cacheDir, source, fmt.Sprintf("Page %d", index))
	}
	file := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(file, []byte("# Notes\n\n"+strings.Repeat("A local note. ", 40)), 0o600); err != nil {
		t.Fatal(err)
	}
	session := connectHarvesterInProcess(t, newTestService(t, Runtime{CacheDir: cacheDir}))
	result := callRaw(t, session, toolRead, map[string]any{
		"publications": publications, "urls": urls, "files": []string{file},
	})
	blocks := readBlocks(t, result, filepath.Join(cacheDir, "public"))
	want := []struct{ item, heading string }{
		{urls[0], "# Page 0"},
		{urls[1], "# Page 1"},
		{urls[2], "# Page 2"},
		{urls[3], "# Page 3"},
		{file, "# Notes"},
		{publications[0], "# Page 4"},
		{publications[1], "# Page 5"},
	}
	if result.IsError || len(blocks) != len(want) {
		t.Fatalf("isError=%v, %d blocks, want %d:\n%s", result.IsError, len(blocks), len(want), allText(result))
	}
	for index, item := range want {
		header := fmt.Sprintf("=== [%d/%d] %s\n{path}\n%s\n", index+1, len(want), item.item, item.heading)
		if !strings.HasPrefix(blocks[index], header) {
			t.Errorf("block %d = %q, want it to open %q", index+1, blocks[index], header)
		}
	}
}

// TestReadMisplacedItemFailsAloneNamingTheField: each misplaced item is its
// own named error pointing at the right field, and the rest of the call
// still reads.
func TestReadMisplacedItemFailsAloneNamingTheField(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "cache")
	seedPage(t, cacheDir, "https://example.test/ok", "Fine")
	session := connectHarvesterInProcess(t, newTestService(t, Runtime{CacheDir: cacheDir}))
	result := callRaw(t, session, toolRead, map[string]any{
		"urls":         []string{"10.1038/nature14539", "/tmp/paper.pdf", "https://example.test/ok"},
		"files":        []string{"https://example.test/page", "arXiv:1706.03762"},
		"publications": []string{"./paper.pdf", "Attention is all you need"},
	})
	if result.IsError {
		t.Fatalf("one item read, yet the call is an error: %s", allText(result))
	}
	blocks := readBlocks(t, result, filepath.Join(cacheDir, "public"))
	if len(blocks) != 7 {
		t.Fatalf("%d blocks, want 7:\n%s", len(blocks), allText(result))
	}
	for _, check := range []struct {
		block int
		want  string
	}{
		{0, "=== [1/7] 10.1038/nature14539\nerror: this is a DOI; put it in publications."},
		{1, "=== [2/7] /tmp/paper.pdf\nerror: this is a local path; put it in files."},
		{3, "=== [4/7] https://example.test/page\nerror: "},
		{4, "=== [5/7] arXiv:1706.03762\nerror: "},
		{5, "=== [6/7] ./paper.pdf\nerror: "},
		{6, "=== [7/7] Attention is all you need\nerror: "},
	} {
		if !strings.HasPrefix(blocks[check.block], check.want) {
			t.Errorf("block %d = %q, want it to open %q", check.block+1, blocks[check.block], check.want)
		}
	}
	for block, want := range map[int]string{
		3: "put it in urls", 4: "put it in publications", 5: "put it in files", 6: "`harvester_search_literature`",
	} {
		if !strings.Contains(blocks[block], want) {
			t.Errorf("block %d = %q, want it to name %q", block+1, blocks[block], want)
		}
	}
	if !strings.HasPrefix(blocks[2], "=== [3/7] https://example.test/ok\n{path}\n# Fine\n") {
		t.Fatalf("the well-placed url did not read: %q", blocks[2])
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

// TestRenderReadItemKeepsAPublishedFailuresText: the render's second pass over
// a published failure keeps its text — a converter's named failure is never
// re-read as "The title is ambiguous".
func TestRenderReadItemKeepsAPublishedFailuresText(t *testing.T) {
	source := "/tmp/demo/broken-feed.xml"
	published := harvest.PublicFailure(source, harvest.Result{
		Source: source, Kind: "feed",
		Error: "harvestpy conversion failed (ValueError): ValueError: the feed parsed to no title and no items: " +
			"a broken or empty feed; a feedparser fallback on a broken feed is unmeasured (stderr: )",
	})
	text, failed := RenderReadItem(1, 1, source, published, ReadView{})
	if !failed || strings.Contains(text, "ambiguous") || !strings.Contains(text, "a broken or empty feed") {
		t.Fatalf("RenderReadItem = %q (failed %v), want the converter's named failure", text, failed)
	}
}

// TestReadArXivPublicationsReadTheWork: an arXiv id — prefixed, bare, or as an
// arxiv.org abs/pdf URL — in publications reads the work through its arXiv
// DOI under the caller's own item; the same abs URL in urls reads the page.
func TestReadArXivPublicationsReadTheWork(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "cache")
	abs := "https://arxiv.org/abs/1706.03762"
	seedPage(t, cacheDir, "10.48550/arXiv.1706.03762", "Attention Paper")
	seedPage(t, cacheDir, abs, "Abs Landing")
	publications := []string{"arXiv:1706.03762", "1706.03762", abs, "https://arxiv.org/pdf/1706.03762"}
	session := connectHarvesterInProcess(t, newTestService(t, Runtime{CacheDir: cacheDir}))
	result := callRaw(t, session, toolRead, map[string]any{"urls": []string{abs}, "publications": publications})
	blocks := readBlocks(t, result, filepath.Join(cacheDir, "public"))
	if len(blocks) != 1+len(publications) {
		t.Fatalf("%d blocks, want %d:\n%s", len(blocks), 1+len(publications), allText(result))
	}
	if want := "=== [1/5] " + abs + "\n{path}\n# Abs Landing\n"; !strings.HasPrefix(blocks[0], want) {
		t.Fatalf("urls block = %q, want the abs page read as a page", blocks[0])
	}
	for index, item := range publications {
		want := fmt.Sprintf("=== [%d/5] %s\n{path}\n# Attention Paper\n", index+2, item)
		if !strings.HasPrefix(blocks[index+1], want) {
			t.Fatalf("publications[%d] block = %q, want the arXiv work read under %q", index, blocks[index+1], item)
		}
	}
}

// seedArtifact stores source as a fresh cached page whose frontmatter adds
// extra lines (gaps, title, …), so read answers it from the cache.
func seedArtifact(t *testing.T, cacheDir, source, extra, body string) {
	t.Helper()
	path := filepath.Join(cacheDir, harvest.CacheKey(source, "html"))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	raw := "---\nsource: harvester\nurl: " + source + "\nkind: html\nmethod: direct\nfetched_at: " +
		time.Now().UTC().Format(time.RFC3339) + "\n" + extra + "---\n\n" + body
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
}

// readBlocks splits a read answer's one text into its blocks, each with its
// artifact path line replaced by "{path}" once that path is proven to be an
// absolute file in the cache's public store.
func readBlocks(t *testing.T, result *mcp.CallToolResult, publicRoot string) []string {
	t.Helper()
	if result.StructuredContent != nil {
		t.Fatalf("read answered structuredContent: %v", result.StructuredContent)
	}
	if len(result.Content) != 1 {
		t.Fatalf("read answered %d Content items, want one text", len(result.Content))
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("read Content[0] = %T, want text", result.Content[0])
	}
	var blocks []string
	for _, block := range strings.Split(text.Text, "\n\n=== ") {
		lines := strings.Split(strings.TrimPrefix(block, "=== "), "\n")
		if len(lines) > 1 && filepath.IsAbs(lines[1]) {
			if !strings.HasPrefix(lines[1], publicRoot+string(filepath.Separator)) {
				t.Fatalf("path line %q is outside the public store %s", lines[1], publicRoot)
			}
			if _, err := os.Stat(lines[1]); err != nil {
				t.Fatalf("path line %q names no artifact: %v", lines[1], err)
			}
			lines[1] = "{path}"
		}
		blocks = append(blocks, "=== "+strings.Join(lines, "\n"))
	}
	return blocks
}

// TestReadAnswersOneTextBlockPerItem: read answers text alone — one block per
// item in input order, numbered over the whole call, its header the item as
// passed, then the artifact's path, a status line only when the artifact is
// not clean, then the content (or its size when include_content is false).
// An item that failed with no artifact is its header and error line; every
// item failed is an error result, one success keeps the call a result.
func TestReadAnswersOneTextBlockPerItem(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "cache")
	publicRoot := filepath.Join(cacheDir, "public")
	body := "# Clean page\n\n" + strings.Repeat("The stilling well agrees with the gauge. ", 6) + "\n"
	long := "# Long page\n\n" + strings.Repeat("Every reading is logged twice. ", 10) + "\n"
	seedArtifact(t, cacheDir, "https://example.test/clean", "", body)
	seedArtifact(t, cacheDir, "https://example.test/partial", "gaps: login wall; 3 of 9 comments loaded\n", body)
	seedArtifact(t, cacheDir, "https://example.test/long", "", long)
	trimmed := strings.TrimSpace(body)
	missing := filepath.Join(t.TempDir(), "absent.md")
	for _, test := range []struct {
		name    string
		runtime Runtime
		args    map[string]any
		isError bool
		want    []string
	}{
		{
			name: "clean", args: map[string]any{"urls": []string{"https://example.test/clean"}},
			want: []string{"=== [1/1] https://example.test/clean\n{path}\n" + trimmed},
		},
		{
			name: "partial", args: map[string]any{"urls": []string{"https://example.test/partial"}},
			want: []string{"=== [1/1] https://example.test/partial\n{path}\npartial: login wall; 3 of 9 comments loaded\n" + trimmed},
		},
		{
			name: "truncated", runtime: Runtime{MaxInlineChars: 40},
			args: map[string]any{"urls": []string{"https://example.test/long"}},
			want: []string{fmt.Sprintf("=== [1/1] https://example.test/long\n{path}\ntruncated: 40 of %d chars, full text at the path\n%s",
				len([]rune(strings.TrimSpace(long))), string([]rune(long)[:40]))},
		},
		{
			name: "size only", args: map[string]any{"urls": []string{"https://example.test/clean"}, "include_content": false},
			want: []string{fmt.Sprintf("=== [1/1] https://example.test/clean\n{path}\nsize: %d chars, ~%d tokens",
				len([]rune(trimmed)), harvest.EstimateTokens(body))},
		},
		{
			name: "failed", isError: true, args: map[string]any{"urls": []string{"10.1038/nature14539"}},
			want: []string{"=== [1/1] 10.1038/nature14539\nerror: this is a DOI; put it in publications."},
		},
		{
			name: "mixed batch",
			args: map[string]any{
				"urls":         []string{"https://example.test/partial", "/tmp/paper.pdf"},
				"files":        []string{missing},
				"publications": []string{"a title with spaces"},
			},
			want: []string{
				"=== [1/4] https://example.test/partial\n{path}\npartial: login wall; 3 of 9 comments loaded\n" + trimmed,
				"=== [2/4] /tmp/paper.pdf\nerror: this is a local path; put it in files.",
				"=== [3/4] " + missing + "\nerror: ",
				"=== [4/4] a title with spaces\nerror: this looks like a title; find it with `harvester_search_literature`, " +
					"then put its handle in publications.",
			},
		},
		{
			name: "remote", runtime: Runtime{Remote: true},
			args: map[string]any{"urls": []string{"https://example.test/clean"}},
			want: []string{"=== [1/1] https://example.test/clean\n" + trimmed},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime := test.runtime
			runtime.CacheDir = cacheDir
			session := connectHarvesterInProcess(t, newTestService(t, runtime))
			result := callRaw(t, session, toolRead, test.args)
			if result.IsError != test.isError {
				t.Errorf("isError = %v, want %v", result.IsError, test.isError)
			}
			blocks := readBlocks(t, result, publicRoot)
			if len(blocks) != len(test.want) {
				t.Fatalf("%d blocks, want %d:\n%s", len(blocks), len(test.want), strings.Join(blocks, "\n\n"))
			}
			for index, want := range test.want {
				got := blocks[index]
				if strings.HasSuffix(want, "\nerror: ") {
					if !strings.HasPrefix(got, want) || strings.Count(got, "\n") != 1 {
						t.Errorf("block %d = %q, want its header and one error line", index+1, got)
					}
					continue
				}
				if got != want {
					t.Errorf("block %d:\n%s\nwant:\n%s", index+1, got, want)
				}
			}
		})
	}
}

// TestReadWholeCallErrorsAreOneErrorLine: a call read refuses as a whole
// answers one error line, its meaning unchanged.
func TestReadWholeCallErrorsAreOneErrorLine(t *testing.T) {
	session := connectHarvesterInProcess(t, newTestService(t, Runtime{}))
	many := make([]string, 51)
	for index := range many {
		many[index] = fmt.Sprintf("https://example.test/%d", index)
	}
	result := callRaw(t, session, toolRead, map[string]any{"urls": many})
	if text := allText(result); !result.IsError || result.StructuredContent != nil ||
		text != "error: read takes at most 50 items in total across urls, files and publications; this call sent 51" {
		t.Fatalf("51 urls: isError=%v text=%q", result.IsError, text)
	}
}
