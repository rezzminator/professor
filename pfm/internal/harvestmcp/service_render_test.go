package harvestmcp

import (
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/harvest"
)

func TestOracleReceiptRenderers(t *testing.T) {
	listing := renderArchiveListing("/tmp/sample.zip", []harvest.Member{{Name: "a|b.txt", UncompressedSize: 7}})
	if want := `archive(source="/tmp/sample.zip", member="<name>")`; !contains(listing, want) {
		t.Fatalf("archive listing does not teach archive member call: %q", listing)
	}
	if !contains(listing, `| a\|b.txt | 7 | file |`) {
		t.Fatalf("archive listing does not escape table member: %q", listing)
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
