package harvest

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
)

// jinaSnapshotReader is a jina reader that answers a cached snapshot of
// another page unless asked fresh, and asked fresh answers freshStatus with
// freshReply (jinaFreshRender for a fresh render); it records the caching
// opt-out of every request.
func jinaSnapshotReader(
	source string, freshStatus int, freshReply string, asked *[]string, mu *sync.Mutex,
) roundTripFunc {
	return func(request *http.Request) (*http.Response, error) {
		noCache := request.Header.Get(jinaNoCacheHeader)
		mu.Lock()
		*asked = append(*asked, request.Header.Get(readerHTMLFormat)+"|"+noCache)
		mu.Unlock()
		if noCache == jinaNoCacheValue {
			if freshStatus != http.StatusOK {
				return response(request, freshStatus, "text/plain", "upstream render failed"), nil
			}
			if request.Header.Get(readerHTMLFormat) == "html" {
				return response(request, http.StatusOK, "text/html; charset=utf-8",
					"<html><body><article>FRESH RENDER "+landingArticle+"</article></body></html>"), nil
			}
			return response(request, http.StatusOK, "text/plain; charset=utf-8", freshReply), nil
		}
		if request.Header.Get(readerHTMLFormat) == "html" {
			return response(request, http.StatusOK, "text/html; charset=utf-8",
				"<html><body><article>POISONED SNAPSHOT "+landingArticle+"</article></body></html>"), nil
		}
		return response(request, http.StatusOK, "text/plain; charset=utf-8", jinaSnapshot(source)), nil
	}
}

// jinaFreshRender is the jina reader's fresh render of source.
func jinaFreshRender(source string) string {
	return "Title: Fresh\n\nURL Source: " + source + "\n\nMarkdown Content:\nFRESH RENDER " + landingArticle
}

// jinaSnapshot is the jina reader's cached snapshot of another page, answered
// under source.
func jinaSnapshot(source string) string {
	return "Title: Test Document\n\nURL Source: " + source + "\n\nWarning: This is a cached snapshot of the original page, " +
		"consider retry with caching opt-out.\n\nMarkdown Content:\nPOISONED SNAPSHOT " + landingArticle
}

// TestJinaCachedSnapshotIsReadFresh: the jina reader answering a cached
// snapshot (here another page's copy) is asked again with its caching
// opt-out, and the fresh render is what is stored, its HTML check asked
// fresh too; a fresh render that fails or answers no content keeps the
// snapshot and names it, and a fresh answer that is still a snapshot is
// named one.
func TestJinaCachedSnapshotIsReadFresh(t *testing.T) {
	t.Parallel()
	const source = "https://pages.example.com/notice"
	origin := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusForbidden, "text/html", "<html><body>Access Denied</body></html>"), nil
	})
	for _, tc := range []struct {
		name        string
		freshStatus int
		freshReply  string
		want        string
		wantPartial string
	}{
		{"fresh render stored", http.StatusOK, jinaFreshRender(source), "FRESH RENDER", ""},
		{
			"fresh render failed", http.StatusServiceUnavailable, "", "POISONED SNAPSHOT",
			"the jina reader answered a cached snapshot and its fresh render failed (it answered HTTP 503)",
		},
		{
			"fresh render empty", http.StatusOK, "Title: Fresh\n\nURL Source: " + source + "\n\nMarkdown Content:\n",
			"POISONED SNAPSHOT",
			"the jina reader answered a cached snapshot and its fresh render failed (it answered no content)",
		},
		{
			"fresh answer still a snapshot", http.StatusOK, jinaSnapshot(source), "POISONED SNAPSHOT",
			"the jina reader answered a cached snapshot despite its caching opt-out",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var asked []string
			var mu sync.Mutex
			h := landingHarvester(t, origin, jinaSnapshotReader(source, tc.freshStatus, tc.freshReply, &asked, &mu))
			result := h.FetchWithOptions(context.Background(), source, FetchOptions{})
			if result.Error != "" || result.Method != "jina" {
				t.Fatalf("the reader page was not stored (method %q): %q", result.Method, result.Error)
			}
			if !strings.Contains(result.Content, tc.want) {
				t.Fatalf("stored content lacks %q:\n%.200s", tc.want, result.Content)
			}
			if tc.wantPartial == "" && strings.Contains(result.Content, "POISONED") {
				t.Fatalf("the cached snapshot was stored:\n%.200s", result.Content)
			}
			if !strings.Contains(result.Partial, tc.wantPartial) {
				t.Fatalf("partial %q lacks %q", result.Partial, tc.wantPartial)
			}
			if tc.wantPartial == "" && strings.Contains(result.Partial, "cached snapshot") {
				t.Fatalf("a fresh render was named a snapshot: %q", result.Partial)
			}
			mu.Lock()
			defer mu.Unlock()
			if tc.wantPartial == "" && !slices.Contains(asked, "html|true") { // the fresh render stored
				t.Fatalf("the HTML check was not asked fresh: %v", asked)
			}
		})
	}
}

// TestJinaRefreshAsksFreshFromTheStart: a refreshing fetch never takes the
// reader's cached answer: its first reader request carries the opt-out, and
// a fresh answer that is still a snapshot is named one.
func TestJinaRefreshAsksFreshFromTheStart(t *testing.T) {
	t.Parallel()
	const source = "https://pages.example.com/notice"
	origin := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusForbidden, "text/html", "<html><body>Access Denied</body></html>"), nil
	})
	for _, tc := range []struct {
		name        string
		freshReply  string
		want        string
		wantPartial string
	}{
		{"fresh render stored", jinaFreshRender(source), "FRESH RENDER", ""},
		{
			"fresh answer still a snapshot", jinaSnapshot(source), "POISONED SNAPSHOT",
			"the jina reader answered a cached snapshot despite its caching opt-out",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var asked []string
			var mu sync.Mutex
			h := landingHarvester(t, origin, jinaSnapshotReader(source, http.StatusOK, tc.freshReply, &asked, &mu))
			result := h.FetchWithOptions(context.Background(), source, FetchOptions{Refresh: true})
			if result.Error != "" || !strings.Contains(result.Content, tc.want) {
				t.Fatalf("refresh did not store %q (error %q):\n%.200s", tc.want, result.Error, result.Content)
			}
			if !strings.Contains(result.Partial, tc.wantPartial) {
				t.Fatalf("partial %q lacks %q", result.Partial, tc.wantPartial)
			}
			if tc.wantPartial == "" && strings.Contains(result.Partial, "cached snapshot") {
				t.Fatalf("a fresh render was named a snapshot: %q", result.Partial)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(asked) == 0 || asked[0] != "|true" {
				t.Fatalf("the first reader request was not asked fresh: %v", asked)
			}
		})
	}
}
