package harvestmcp

import (
	"archive/zip"
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
// message must recommend `webSearch` only when a backend is actually
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
	for _, want := range []string{"no readable content", "`webSearch`", "`findWorks`"} {
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
	if strings.Contains(got, "`webSearch`") {
		t.Fatalf("search-off describe receipt names the unavailable `webSearch` tool: %q", got)
	}
	if !strings.Contains(got, "`findWorks`") {
		t.Fatalf("search-off describe receipt missing findWorks fallback: %q", got)
	}
}

// TestDescribeFetchNamesAPartialArtifact pins the MCP receipt header: a
// known-incomplete artifact says so after `path: …` on the header line, and a
// complete one carries no PARTIAL notice.
func TestDescribeFetchNamesAPartialArtifact(t *testing.T) {
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
	if !strings.Contains(complete, "path: "+path) || strings.Contains(complete, "PARTIAL:") {
		t.Fatalf("complete receipt missing its header or naming a PARTIAL notice:\n%s", complete)
	}
	result.Partial = "page 3 of 9 failed to convert"
	got := service.describeFetch("https://fixture.example/source", result, false)
	want := "path: " + path + " / PARTIAL: page 3 of 9 failed to convert\n\nbody text"
	if !strings.Contains(got, want) {
		t.Fatalf("partial receipt:\n%s\nwant it to contain:\n%s", got, want)
	}
	// The size probe is a receipt too: a caller budgeting a read must learn
	// the artifact is incomplete before it reads it.
	if size := service.describeFetch("https://fixture.example/source", result, true); !strings.Contains(
		size,
		`"partial":"page 3 of 9 failed to convert"`,
	) {
		t.Fatalf("size-only receipt hides the partial reason:\n%s", size)
	}
	result.Partial = ""
	complete = service.describeFetch("https://fixture.example/source", result, true)
	if strings.Contains(complete, "partial") {
		t.Fatalf("complete size-only receipt names a partial reason:\n%s", complete)
	}
}
