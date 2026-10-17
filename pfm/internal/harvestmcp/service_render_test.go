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

func TestRenderReadItemLegacyFailureKindsNameTheSameRecovery(t *testing.T) {
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
			got, failed := RenderReadItem(1, 1, "https://fixture.example/source", test.result, ReadView{})
			if !failed || !strings.HasPrefix(got, "=== [1/1] https://fixture.example/source\nerror: ") {
				t.Fatalf("failure block = %q, want the header and an error line", got)
			}
			for _, want := range test.want {
				if !strings.Contains(got, want) {
					t.Fatalf("failure block missing %q: %q", want, got)
				}
			}
		})
	}
}

// TestRenderReadItemThinExtractionNamesSearchOnlyWhenAvailable: the "thin
// extraction" (JS-rendered/bot-blocked, no readable content) message
// recommends `search_web` only when a backend is configured, and
// falls back to search_literature/another-URL wording when it is not.
func TestRenderReadItemThinExtractionNamesSearchOnlyWhenAvailable(t *testing.T) {
	result := harvest.Result{HTTPStatus: 200}
	got, _ := RenderReadItem(1, 1, "https://fixture.example/source", result, ReadView{SearchEnabled: true})
	for _, want := range []string{"no readable content", "`search_web`", "`search_literature`"} {
		if !strings.Contains(got, want) {
			t.Fatalf("search-on failure block missing %q: %q", want, got)
		}
	}
	got, _ = RenderReadItem(1, 1, "https://fixture.example/source", result, ReadView{})
	if strings.Contains(got, "`search_web`") || !strings.Contains(got, "`search_literature`") {
		t.Fatalf("search-off failure block = %q, want search_literature and no search_web", got)
	}
}

// TestRenderReadItemNamesTheGapsOfAnIncompleteArtifact: a known-incomplete
// artifact says so on its own status line, in a full read and a size probe
// alike (a caller budgeting a read learns it before it reads); a complete one
// carries none.
func TestRenderReadItemNamesTheGapsOfAnIncompleteArtifact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.md")
	result := harvest.Result{
		HTTPStatus: 200, CacheStatus: "miss", Bytes: 9, Chars: 9, Tokens: 3, Path: path, Content: "body text",
	}
	for _, test := range []struct {
		partial string
		view    ReadView
		want    string
	}{
		{"", ReadView{}, "=== [1/1] https://fixture.example/source\n" + path + "\nbody text"},
		{
			"page 3 of 9 failed to convert; 2 image(s) could not be published",
			ReadView{},
			"=== [1/1] https://fixture.example/source\n" + path +
				"\npartial: page 3 of 9 failed to convert; 2 image(s) could not be published\nbody text",
		},
		{
			"page 3 of 9 failed to convert",
			ReadView{SizeOnly: true},
			"=== [1/1] https://fixture.example/source\n" + path +
				"\npartial: page 3 of 9 failed to convert\nsize: 9 chars, ~3 tokens",
		},
	} {
		result.Partial = test.partial
		if got, failed := RenderReadItem(1, 1, "https://fixture.example/source", result, test.view); failed ||
			got != test.want {
			t.Errorf("partial %q size-only %v:\n%s\nwant:\n%s", test.partial, test.view.SizeOnly, got, test.want)
		}
	}
}

// TestRenderReadItemSizeProbeFailsAThinErrorPageLikeTheFullRead: a size probe (the default of a read
// with `ask`) blanks the content, so it judges a thin HTTP error page by its char count and status
// as the full read judges it by its text: under 500 chars at status 400 or above is a failure
// block, not a size line for the answering model to treat as a good file; a thin page that was
// answered 200 and a long page that was answered 404 stay size lines.
func TestRenderReadItemSizeProbeFailsAThinErrorPageLikeTheFullRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.pdf")
	source := "https://fixture.example/source.pdf"
	for _, test := range []struct {
		name         string
		status       int
		chars        int
		wantFailed   bool
		wantContains string
	}{
		{"thin page answered 404", 404, 120, true, harvest.PublicFailureMessage(harvest.Result{HTTPStatus: 404})},
		{"thin page answered 200", 200, 120, false, "size: 120 chars"},
		{"long page answered 404", 404, 900, false, "size: 900 chars"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := harvest.Result{
				HTTPStatus: test.status, Bytes: 4096, Chars: test.chars, Tokens: 30, Path: path,
			}
			got, failed := RenderReadItem(1, 1, source, result, ReadView{SizeOnly: true})
			if failed != test.wantFailed || !strings.Contains(got, test.wantContains) {
				t.Fatalf(
					"failed=%v block %q, want failed=%v containing %q",
					failed,
					got,
					test.wantFailed,
					test.wantContains,
				)
			}
			if test.wantFailed && strings.Contains(got, "size:") {
				t.Fatalf("a failed probe still renders a size line: %q", got)
			}
		})
	}
}

// TestRenderFindNamesTheHarvesterSearchTool: an empty candidate list points
// at search_web.
func TestRenderFindNamesTheHarvesterSearchTool(t *testing.T) {
	text := RenderFind("an unknown title", FindOutput{})
	if !strings.Contains(text, "search_web") {
		t.Fatalf("RenderFind empty hint = %q, want search_web", text)
	}
}
