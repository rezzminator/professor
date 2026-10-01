package harvest

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
)

// TestWaybackCopyIsNamedAsTheArchive: a page the origin refused and the
// Wayback Machine served is stored under the original address as the
// archive's copy — its method the wayback rung and its partial naming the
// snapshot's date — never labelled as the live page the direct rung read.
func TestWaybackCopyIsNamedAsTheArchive(t *testing.T) {
	t.Parallel()
	const source = "https://directory.example.test/people/avery-example"
	const snapshot = "https://web.archive.org/web/20150928080310id_/" + source
	page := "<html><body><h1>Avery Example</h1>" +
		strings.Repeat("<p>A stilling well keeps the river gauge's reading steady against the current.</p>", 30) +
		"</body></html>"
	served := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() == snapshot {
			return response(request, http.StatusOK, "text/html; charset=UTF-8", page), nil
		}
		return response(request, http.StatusForbidden, "text/html", "<html><body>denied</body></html>"), nil
	})
	archive := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "archive.org" {
			return jsonResponse(
				request,
				`{"archived_snapshots":{"closest":{"available":true,"timestamp":"20150928080310"}}}`,
			), nil
		}
		return jsonResponse(request, `{}`), nil
	})
	h := mustNew(t, Options{
		CacheDir: t.TempDir(),
		Client:   &http.Client{Transport: served},
		Chrome:   &http.Client{Transport: served},
		Jina:     &http.Client{Transport: served},
		OA:       &http.Client{Transport: archive},
		Converter: legacyConverterFunc(
			func(_ context.Context, _, _ string, raw []byte) (string, error) {
				return htmlTagRe.ReplaceAllString(string(raw), " "), nil
			},
		),
		BrowserRung: browserOff(),
		Clock:       newPacingClock(),
	})
	result := h.FetchWithOptions(context.Background(), source, FetchOptions{Refresh: true})
	if result.Error != "" {
		t.Fatalf("the archived copy was not returned: %s", result.Error)
	}
	if result.Source != source || result.Method != rungWayback {
		t.Fatalf("source %q method %q, want %q read through %q", result.Source, result.Method, source, rungWayback)
	}
	const want = "archived copy: the Wayback Machine's snapshot of 2015-09-28, not the live page"
	if !strings.Contains(result.Partial, want) {
		t.Fatalf("partial = %q, want it to name %q", result.Partial, want)
	}
	stored, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatalf("read the stored artifact %s: %v", result.Path, err)
	}
	if !strings.Contains(string(stored), partialMarkerPrefix+want) ||
		!strings.Contains(string(stored), "stilling well") {
		t.Fatalf("the stored artifact does not open with the archive's note:\n%.400s", stored)
	}
}
