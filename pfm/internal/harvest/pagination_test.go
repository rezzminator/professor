package harvest

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// TestAnUnfollowedNextPageFlagsAGenericPagePartial: a page no extractor
// claims links its own next page. The artifact is flagged partial naming the
// continuation — never silently the first page alone — and no browser render
// is spent on it (a render is the same page). A rel="next" to a different
// page (a blog's adjacent post) is not a continuation.
func TestAnUnfollowedNextPageFlagsAGenericPagePartial(t *testing.T) {
	article := strings.Repeat("A long paragraph of the article's own text, with enough words to count. ", 20)
	for _, tc := range []struct {
		name, source, next, gap string
	}{
		{"query page", "https://blog.example.test/guide", "/guide?page=2&session=s3cr3t", "guide?page=2&session=…"},
		{"path page", "https://blog.example.test/guide/", "/guide/2/", "https://blog.example.test/guide/2/"},
		{"second page", "https://blog.example.test/threads/t.12/page-2", "/threads/t.12/page-3", "page-3"},
		{"adjacent post", "https://blog.example.test/2026/03/guide/", "/2026/03/another-guide/", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := `<html><head><title>Guide</title><link rel="next" title="Next" href="` + tc.next + `"></head>` +
				`<body><main><h1>Guide</h1><p>` + article + `</p></main></body></html>`
			spy := &browserSpyConverter{html: page, status: http.StatusOK, convertFn: (&fakeConverter{}).Convert}
			h := pageHarvester(t, page, spy, browserOn())
			result := h.Fetch(context.Background(), tc.source)
			if result.Error != "" || result.Method != rungDirect || spy.browserCalls != 0 {
				t.Fatalf("page not kept at the direct rung: method=%q rungs=%v browser=%d error=%q",
					result.Method, result.Rungs, spy.browserCalls, result.Error)
			}
			if tc.gap == "" {
				if result.Partial != "" {
					t.Fatalf("a link to another page flagged the artifact partial: %q", result.Partial)
				}
				return
			}
			if !strings.Contains(result.Partial, `rel="next"`) || !strings.Contains(result.Partial, tc.gap) ||
				strings.Contains(result.Partial, "s3cr3t") {
				t.Fatalf("the continuation is not named (or leaks a query value): %q", result.Partial)
			}
		})
	}
}
