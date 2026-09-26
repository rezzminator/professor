package harvest

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// wallOverlay is a challenge interstitial laid over a page that keeps the
// thread's markup beneath it: the markup an extractor knows, and a wall.
const wallOverlay = `<div id="cf-overlay"><h1>Just a moment...</h1><p>Checking your browser before accessing.</p></div>`

// overlaid lays wallOverlay over page, inside its body.
func overlaid(page string) string {
	if strings.Contains(page, "</body>") {
		return strings.Replace(page, "</body>", wallOverlay+"</body>", 1)
	}
	return page + wallOverlay
}

// TestAWallIsNeverStoredAsAMarkupExtractorsContent: only an extractor that
// reads its site's API may keep a page the site walled, and only from an API
// record it proved. The captured Stack Exchange challenge served at a Reddit,
// Hacker News, Discourse or GitHub thread's address — alone, or laid over the
// thread's own markup so the site's extractor claims it — is never stored as
// that thread's content.
func TestAWallIsNeverStoredAsAMarkupExtractorsContent(t *testing.T) {
	challenge := seFixture(t, "challenge.html")
	redditURL := "https://www.reddit.com/r/examplesub/comments/ccc333/loader_thread/"
	redditPage := redditThreadHTML(2, []string{"first reply", "second reply"}, false)
	discoursePage := discourseCrawlerPage(discourseFixturePosts(), 1, true)
	for _, tc := range []struct{ name, source, page string }{
		{"reddit, the challenge", redditURL, challenge},
		{"reddit, the thread under a wall", redditURL, overlaid(redditPage)},
		{"hacker news, the challenge", hnThreadURL, challenge},
		{"hacker news, the thread under a wall", hnThreadURL, overlaid(hnFixture(t))},
		{"discourse, the challenge", discourseTopicURL, challenge},
		{"discourse, the topic under a wall", discourseTopicURL, overlaid(discoursePage)},
		{"github, the challenge", ghIssueURL, challenge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !isChallenge([]byte(tc.page), http.StatusForbidden) {
				t.Fatal("the served page is not a wall; the case proves nothing")
			}
			wall := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return response(request, http.StatusForbidden, "text/html; charset=UTF-8", tc.page), nil
			})
			missing := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return response(request, http.StatusNotFound, "application/json", `{}`), nil
			})
			converter := &browserSpyConverter{convertFn: func(context.Context, string, string, []byte) (string, error) {
				return "CONVERTED WALL " + strings.Repeat("text of the served page ", 40), nil
			}}
			h := mustNew(t, Options{
				CacheDir:    t.TempDir(),
				Client:      &http.Client{Transport: wall},
				Chrome:      &http.Client{Transport: wall},
				Jina:        &http.Client{Transport: missing},
				OA:          &http.Client{Transport: missing},
				Converter:   converter,
				BrowserRung: browserOff(),
				Clock:       newPacingClock(),
			})
			result := h.FetchWithOptions(context.Background(), tc.source, FetchOptions{Refresh: true})
			if result.Error == "" {
				t.Fatalf("a wall was stored as content (method %q):\n%.600s", result.Method, result.Content)
			}
		})
	}
}
