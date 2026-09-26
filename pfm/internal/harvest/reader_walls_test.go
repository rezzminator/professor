package harvest

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readerWallFixture reads a captured reader response from testdata/walls.
func readerWallFixture(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "walls", name))
	if err != nil {
		t.Fatalf("read the fixture %s: %v", name, err)
	}
	return string(body)
}

// TestReaderPagesPassTheWallCheck: a page only a reader rung served passes
// the same wall check as an HTTP rung's page. A paywalled article Jina served
// as Markdown is stored partial naming the paywall its HTML flags; an article
// whose prose mentions a subscription, its HTML flagging nothing, is stored
// whole; a logged-out app page names its login wall; a check whose HTML the
// reader could not serve is named, never stored as no wall; and a paywall an earlier
// rung's HTML showed is named on the reader's page when the reader's HTML
// cannot be read.
func TestReaderPagesPassTheWallCheck(t *testing.T) {
	const paywallURL = "https://www.bloomberg.com/news/articles/2025-10-22/reddit-sues-perplexity-others-over-alleged-data-scraping"
	walledOrigin := `<html><head><script type="application/ld+json">{"@type":"Article","isAccessibleForFree":false}</script>` +
		`</head><body><p>Access to this page has been denied.</p></body></html>`
	for _, tc := range []struct {
		name, source, origin, markdown, readerHTML, partial string
	}{
		{
			name:       "a paywalled article the reader served",
			source:     paywallURL,
			origin:     "<html><body><p>Access to this page has been denied.</p></body></html>",
			markdown:   readerWallFixture(t, "reader-paywalled.md"),
			readerHTML: readerWallFixture(t, "reader-paywalled.html"),
			partial:    paywallReason,
		},
		{
			name:       "a clean article that mentions a subscription",
			source:     "https://simonwillison.net/2024/Dec/31/llms-in-2024/",
			origin:     "<html><body><p>Access to this page has been denied.</p></body></html>",
			markdown:   readerWallFixture(t, "reader-clean.md"),
			readerHTML: readerWallFixture(t, "reader-clean.html"),
		},
		{
			name:       "a logged-out app page the reader served",
			source:     "https://www.linkedin.com/company/microsoft",
			origin:     "<html><body><p>Access to this page has been denied.</p></body></html>",
			markdown:   readerWallFixture(t, "reader-loginwall.md"),
			readerHTML: readerWallFixture(t, "reader-loginwall.html"),
			partial:    loginWallReason,
		},
		{
			name:     "a wall check the reader's HTML could not serve",
			source:   paywallURL,
			origin:   "<html><body><p>Access to this page has been denied.</p></body></html>",
			markdown: readerWallFixture(t, "reader-paywalled.md"),
			partial:  "its wall check could not run (the reader answered HTTP 503)",
		},
		{
			name:     "a paywall only the origin's refused HTML showed",
			source:   paywallURL,
			origin:   walledOrigin,
			markdown: readerWallFixture(t, "reader-paywalled.md"),
			partial:  paywallReason,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			origin := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return response(request, http.StatusForbidden, "text/html; charset=UTF-8", tc.origin), nil
			})
			htmlAsked := false
			reader := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.Header.Get(readerHTMLFormat) == "html" {
					htmlAsked = true
					if tc.readerHTML == "" {
						return response(request, http.StatusServiceUnavailable, "text/plain", "busy"), nil
					}
					return response(request, http.StatusOK, "text/html; charset=utf-8", tc.readerHTML), nil
				}
				return response(request, http.StatusOK, "text/plain; charset=utf-8", tc.markdown), nil
			})
			converter := &browserSpyConverter{convertFn: func(context.Context, string, string, []byte) (string, error) {
				return "Access to this page has been denied.", nil
			}}
			h := mustNew(t, Options{
				CacheDir:    t.TempDir(),
				Client:      &http.Client{Transport: origin},
				Chrome:      &http.Client{Transport: origin},
				Jina:        &http.Client{Transport: reader},
				OA:          &http.Client{Transport: origin},
				Converter:   converter,
				BrowserRung: browserOff(),
				Clock:       newPacingClock(),
			})
			result := h.FetchWithOptions(context.Background(), tc.source, FetchOptions{Refresh: true})
			if result.Error != "" || result.Method != "jina" {
				t.Fatalf("the reader's page was not stored (method %q): %s", result.Method, result.Error)
			}
			switch {
			case tc.partial == "" && result.Partial != "":
				t.Fatalf("a clean reader article was flagged partial: %q", result.Partial)
			case tc.partial != "" && !strings.Contains(result.Partial, tc.partial):
				t.Fatalf("the reader's page is partial %q, want it to name %q", result.Partial, tc.partial)
			}
			if !htmlAsked {
				t.Fatal("the reader's HTML was never asked for; the wall check did not run on the reader's page")
			}
		})
	}
}

// TestReaderPagesPassTheStatedCountAndRecallChecks: a page only a reader rung
// served passes the same stated-count check and recall gate as an HTTP rung's
// page, on the reader's own HTML of it: a thread whose structured data states
// more comments than it carries is stored partial naming both counts, and
// markdown keeping a fraction of the page's visible text names what it kept.
func TestReaderPagesPassTheStatedCountAndRecallChecks(t *testing.T) {
	const source = "https://forum.example.com/t/how-teams-use-ai-4g9c"
	article := strings.Repeat("The newspaper sued two technology companies for copyright infringement on Wednesday, "+
		"opening a new front.\n\n", 8)
	for _, tc := range []struct {
		name, markdown, readerHTML, partial string
	}{
		{
			name:     "a thread stating 50 comments and carrying 2",
			markdown: "# How teams use AI\n\n" + article,
			readerHTML: `<html><head><script type="application/ld+json">{"@type":"DiscussionForumPosting",` +
				`"commentCount":50,"comment":[` + commentItems(2) + `]}</script></head><body><article>` +
				articlePreview + `</article></body></html>`,
			partial: "50 comments stated · 2 loaded",
		},
		{
			name:     "markdown keeping one paragraph of a long page",
			markdown: "# How teams use AI\n\n" + article,
			readerHTML: `<html><body><article>` + articlePreview + strings.Repeat(
				`<p>Readers across the region described how their teams adopted new tools during the long winter.</p>`,
				40) + `</article></body></html>`,
			partial: "the reader's markdown kept",
		},
		{
			name:       "markdown keeping the whole page",
			markdown:   "# How teams use AI\n\n" + article,
			readerHTML: `<html><body><article>` + articlePreview + `</article></body></html>`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			origin := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return response(request, http.StatusForbidden, "text/html; charset=UTF-8",
					"<html><body><p>Access to this page has been denied.</p></body></html>"), nil
			})
			reader := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.Header.Get(readerHTMLFormat) == "html" {
					return response(request, http.StatusOK, "text/html; charset=utf-8", tc.readerHTML), nil
				}
				return response(request, http.StatusOK, "text/plain; charset=utf-8", tc.markdown), nil
			})
			h := mustNew(t, Options{
				CacheDir: t.TempDir(),
				Client:   &http.Client{Transport: origin},
				Chrome:   &http.Client{Transport: origin},
				Jina:     &http.Client{Transport: canonicalReader(reader)},
				OA:       &http.Client{Transport: origin},
				Converter: &browserSpyConverter{
					convertFn: func(context.Context, string, string, []byte) (string, error) {
						return "Access to this page has been denied.", nil
					},
				},
				BrowserRung: browserOff(),
				Clock:       newPacingClock(),
			})
			result := h.FetchWithOptions(context.Background(), source, FetchOptions{Refresh: true})
			if result.Error != "" || result.Method != "jina" {
				t.Fatalf("the reader's page was not stored (method %q): %s", result.Method, result.Error)
			}
			switch {
			case tc.partial == "" && result.Partial != "":
				t.Fatalf("a whole reader page was flagged partial: %q", result.Partial)
			case tc.partial != "" && !strings.Contains(result.Partial, tc.partial):
				t.Fatalf("the reader's page is partial %q, want it to name %q", result.Partial, tc.partial)
			}
		})
	}
}
