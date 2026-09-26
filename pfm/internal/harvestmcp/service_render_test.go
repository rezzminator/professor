package harvestmcp

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/harvest"
)

// writeTestZip creates a minimal zip archive at path for a redaction test to
// list.
func writeTestZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	writer := zip.NewWriter(file)
	for name, body := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDescribeLegacyFailureKindsNameTheSameRecovery(t *testing.T) {
	tests := []struct {
		name   string
		result harvest.Result
		want   []string
	}{
		{
			name:   "invalid URL",
			result: harvest.Result{ErrorKind: "invalid"},
			want:   []string{"input is invalid", "search_literature"},
		},
		{
			name:   "timeout",
			result: harvest.Result{ErrorKind: "timeout"},
			want:   []string{"timed out", "Retry later"},
		},
		{
			name:   "challenge",
			result: harvest.Result{Challenge: true, HTTPStatus: 200},
			want:   []string{"access challenge", "another copy"},
		},
		{
			name:   "HTTP 404",
			result: harvest.Result{Content: "tiny", ContentChars: 4, HTTPStatus: 404},
			want:   []string{"not found", "search_literature"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := (*Service)(nil).describeFetch("https://fixture.example/source", test.result, false)
			for _, want := range test.want {
				if !strings.Contains(got, want) {
					t.Fatalf("describe receipt missing %q: %q", want, got)
				}
			}
		})
	}
}

// TestDescribeThinExtractionNamesSearchOnlyWhenAvailable is
// TestDescribeLegacyFailureKindsNameTheSameRecovery's search-gated sibling:
// the "thin extraction" (JS-rendered/bot-blocked, no readable content)
// message must recommend `harvester_search_web` only when a backend is
// actually configured, and fall back to harvester_search_literature/another-URL wording when it is not.
func TestDescribeThinExtractionNamesSearchOnlyWhenAvailable(t *testing.T) {
	result := harvest.Result{HTTPStatus: 200}

	searchOn, err := NewConfiguredHarvester(
		"test",
		Runtime{
			Home:       t.TempDir(),
			CacheDir:   filepath.Join(t.TempDir(), "cache"),
			SearXNGURL: "http://searxng.example.test",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = searchOn.Close() }()
	got := searchOn.describeFetch("https://fixture.example/source", result, false)
	for _, want := range []string{"no readable content", "`harvester_search_web`", "`harvester_search_literature`"} {
		if !strings.Contains(got, want) {
			t.Fatalf("search-on describe receipt missing %q: %q", want, got)
		}
	}

	searchOff, err := NewConfiguredHarvester(
		"test",
		Runtime{Home: t.TempDir(), CacheDir: filepath.Join(t.TempDir(), "cache")},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = searchOff.Close() }()
	got = searchOff.describeFetch("https://fixture.example/source", result, false)
	if strings.Contains(got, "`harvester_search_web`") {
		t.Fatalf("search-off describe receipt names the unavailable `harvester_search_web` tool: %q", got)
	}
	if !strings.Contains(got, "`harvester_search_literature`") {
		t.Fatalf("search-off describe receipt missing harvester_search_literature fallback: %q", got)
	}
}

// TestSizeOnlyReceiptNamesTokensNotSize: the size probe names its token
// count `tokens`; no `size` key holds it, so nothing reads it as bytes.
func TestSizeOnlyReceiptNamesTokensNotSize(t *testing.T) {
	service := newTestService(t, Runtime{})
	result := harvest.Result{
		HTTPStatus:  200,
		CacheStatus: "hit",
		Chars:       40,
		Bytes:       90,
		Tokens:      12,
		Path:        filepath.Join(t.TempDir(), "source.md"),
		Content:     "body",
	}
	text := service.describeFetch("https://fixture.example/source", result, true)
	var receipt map[string]any
	if err := json.Unmarshal([]byte(text), &receipt); err != nil {
		t.Fatalf("size-only receipt is not JSON: %v\n%s", err, text)
	}
	if _, found := receipt["size"]; found {
		t.Fatalf("size-only receipt carries a `size` key: %s", text)
	}
	if receipt["tokens"] != float64(12) || receipt["chars"] != float64(40) {
		t.Fatalf("size-only receipt tokens/chars = %v/%v, want 12/40: %s", receipt["tokens"], receipt["chars"], text)
	}
}

// TestReadItemCarriesStatusOnEveryRead: a cached and a fresh result, read as
// a page or a work, both carry their status on the typed item.
func TestReadItemCarriesStatusOnEveryRead(t *testing.T) {
	service := newTestService(t, Runtime{})
	for _, cache := range []string{"hit", "miss"} {
		for _, field := range readFields {
			item := service.readItem(readJob{field: field, source: "https://fixture.example/source"},
				harvest.Result{HTTPStatus: 200, CacheStatus: cache, Kind: "html", Content: "body"}, true)
			if item.Status != 200 {
				t.Fatalf("cache %s field %s: item status = %d, want 200", cache, field, item.Status)
			}
		}
	}
}

// TestDescribeFetchNamesTheGapsOfAnIncompleteArtifact pins the MCP receipt header: a
// known-incomplete artifact says so after `path: …` on the header line, and a
// complete one carries no gaps notice.
func TestDescribeFetchNamesTheGapsOfAnIncompleteArtifact(t *testing.T) {
	service, err := NewConfiguredHarvester(
		"test",
		Runtime{Home: t.TempDir(), CacheDir: filepath.Join(t.TempDir(), "cache")},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = service.Close() }()
	path := filepath.Join(t.TempDir(), "source.md")
	result := harvest.Result{
		HTTPStatus:  200,
		CacheStatus: "miss",
		Bytes:       9,
		Tokens:      3,
		Path:        path,
		Content:     "body text",
	}
	complete := service.describeFetch("https://fixture.example/source", result, false)
	if !strings.Contains(complete, "path: "+path) || strings.Contains(complete, "gaps:") {
		t.Fatalf("complete receipt missing its header or naming a gaps notice:\n%s", complete)
	}
	result.Partial = "page 3 of 9 failed to convert"
	got := service.describeFetch("https://fixture.example/source", result, false)
	want := "path: " + path + " / gaps: page 3 of 9 failed to convert\n\nbody text"
	if !strings.Contains(got, want) {
		t.Fatalf("partial receipt:\n%s\nwant it to contain:\n%s", got, want)
	}
	// The size probe is a receipt too: a caller budgeting a read must learn
	// the artifact is incomplete before it reads it.
	if size := service.describeFetch("https://fixture.example/source", result, true); !strings.Contains(
		size,
		`"gaps":["page 3 of 9 failed to convert"]`,
	) {
		t.Fatalf("size-only receipt hides the gaps:\n%s", size)
	}
	result.Partial = ""
	complete = service.describeFetch("https://fixture.example/source", result, true)
	if strings.Contains(complete, "gaps") {
		t.Fatalf("complete size-only receipt names a gap:\n%s", complete)
	}
}

// TestSizeOnlyReceiptUsesTheItemFieldNames: the include_content:false text
// receipt names what the typed item names — cached, tokens, chars, path, via,
// kind — and no old field name.
func TestSizeOnlyReceiptUsesTheItemFieldNames(t *testing.T) {
	service := newTestService(t, Runtime{})
	result := harvest.Result{
		HTTPStatus: 200, CacheStatus: "hit", Kind: "pdf", Method: "arxiv", Chars: 40, Tokens: 12,
		Path: filepath.Join(t.TempDir(), "source.md"), Content: "body",
	}
	text := service.describeFetch("arXiv:1706.03762", result, true)
	var receipt map[string]any
	if err := json.Unmarshal([]byte(text), &receipt); err != nil {
		t.Fatalf("size-only receipt is not JSON: %v\n%s", err, text)
	}
	for _, old := range []string{"cache_status", "token_count", "size", "method"} {
		if _, found := receipt[old]; found {
			t.Fatalf("size-only receipt carries the old field %q: %s", old, text)
		}
	}
	if receipt["cached"] != true || receipt["via"] != "arxiv" || receipt["kind"] != "pdf" ||
		receipt["tokens"] != float64(12) || receipt["chars"] != float64(40) || receipt["path"] != result.Path {
		t.Fatalf("size-only receipt = %s, want cached/via/kind/tokens/chars/path of the item", text)
	}
}

// TestRenderFindNamesTheHarvesterSearchTool: an empty candidate list points
// at harvester_search_web, never the retired WebSearch name.
func TestRenderFindNamesTheHarvesterSearchTool(t *testing.T) {
	text := renderFind("an unknown title", nil, nil)
	if strings.Contains(text, "WebSearch") || !strings.Contains(text, "harvester_search_web") {
		t.Fatalf("renderFind empty hint = %q, want harvester_search_web and no WebSearch", text)
	}
}

// TestFullReadHeaderUsesTheItemFieldNames: the full-read text header names
// what the typed item names — cached, tokens — and never cache_status.
func TestFullReadHeaderUsesTheItemFieldNames(t *testing.T) {
	service := newTestService(t, Runtime{})
	result := harvest.Result{
		HTTPStatus: 200, CacheStatus: "miss", Bytes: 700, Tokens: 12,
		Path: filepath.Join(t.TempDir(), "source.md"), Content: strings.Repeat("readable body ", 50),
	}
	text := service.describeFetch("https://fixture.example/page", result, false)
	want := "# https://fixture.example/page\n" +
		"cached: false / bytes: 700 / tokens: 12 / fetched_at: unknown / path: " + result.Path
	if !strings.HasPrefix(text, want) {
		t.Fatalf("full-read header:\n%s\nwant it to open with:\n%s", text, want)
	}
	if strings.Contains(text, "cache_status") {
		t.Fatalf("full-read header carries cache_status:\n%s", text)
	}
}
