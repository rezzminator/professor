package harvestmcp

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

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

// TestArchiveToolNeverEchoesALocalPathInItsListing is L2-F19 end to end: a
// local archive the caller is permitted to list (inside LocalRoots) renders
// its listing without the raw filesystem path — the same redaction
// PublicResult already applies to the exported file's own source.
func TestArchiveToolNeverEchoesALocalPathInItsListing(t *testing.T) {
	root := t.TempDir()
	zipPath := filepath.Join(root, "bundle.zip")
	writeTestZip(t, zipPath, map[string]string{"a.txt": "hi"})
	service, err := NewConfiguredHarvester(
		"test",
		Runtime{Home: t.TempDir(), CacheDir: filepath.Join(t.TempDir(), "cache"), LocalRoots: []string{root}},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = service.Close() }()
	result, _, err := service.archive(context.Background(), (*mcp.CallToolRequest)(nil), ArchiveInput{Source: zipPath})
	if err != nil {
		t.Fatal(err)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("archive result content = %T, want *mcp.TextContent", result.Content[0])
	}
	if strings.Contains(text.Text, zipPath) || strings.Contains(text.Text, root) {
		t.Fatalf("archive listing echoed the local path: %q", text.Text)
	}
	if !strings.Contains(text.Text, "requested archive") {
		t.Fatalf("archive listing did not use the redacted display source: %q", text.Text)
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
			want:   []string{"input is invalid", "findWorks"},
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
			want:   []string{"not found", "findWorks"},
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
// message must recommend `search` only when a backend is actually
// configured, and fall back to findWorks/another-URL wording when it is not.
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
	for _, want := range []string{"no readable content", "`search`", "`findWorks`"} {
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
	if strings.Contains(got, "`search`") {
		t.Fatalf("search-off describe receipt names the unavailable `search` tool: %q", got)
	}
	if !strings.Contains(got, "`findWorks`") {
		t.Fatalf("search-off describe receipt missing findWorks fallback: %q", got)
	}
}
