package harvest

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// commentItems is n schema.org Comment items as a JSON-LD comment array.
func commentItems(n int) string {
	items := make([]string, n)
	for i := range items {
		items[i] = fmt.Sprintf(`{"@type":"Comment","text":"comment %d"}`, i)
	}
	return strings.Join(items, ",")
}

// TestGenericThreadGapsAreNamed: a page on the generic path whose own markup
// shows more of its thread than it loaded — a stated count in its structured
// data above the items it carries, or an unpressed loader control — is
// flagged partial naming what the page says; prose alone names nothing.
func TestGenericThreadGapsAreNamed(t *testing.T) {
	source := "https://forum.example.com/t/how-teams-use-ai-4g9c"
	for _, tc := range []struct {
		name, page string
		partial    []string
	}{
		{
			name: "a discussion stating 237 comments and carrying 38",
			page: `<html><head><script type="application/ld+json">{"@context":"https://schema.org",` +
				`"@type":"DiscussionForumPosting","headline":"How teams use AI","commentCount":237,"comment":[` +
				commentItems(38) + `]}</script></head><body><article>` + articlePreview + `</article></body></html>`,
			partial: []string{"the page states 237 comments; 38 appear in the page"},
		},
		{
			name: "a thread with a Load more button and a hidden-items button",
			page: `<html><body><article>` + articlePreview + `</article><div class="js-timeline">` +
				`<button type="submit"><span>801 hidden items</span></button>` +
				`<button type="submit" class="ajax-pagination-btn">Load more…</button></div></body></html>`,
			partial: []string{"'801 hidden items'", "'Load more…'", "not pressed"},
		},
		{
			name: "an article saying load more in its prose, with no loader element",
			page: `<html><body><article>` + articlePreview + `<p>Five habits that load more energy into your day, ` +
				`and show more comments from readers below.</p></article></body></html>`,
		},
		{
			name: "a store page stating its reviews and carrying no Review item",
			page: `<html><head><script type="application/ld+json">{"@context":"https://schema.org","@type":"Product",` +
				`"name":"Portal 2","aggregateRating":{"@type":"AggregateRating","ratingValue":"98",` +
				`"reviewCount":"173878"}}</script></head><body><article>` + articlePreview +
				`<div class="user_reviews"><p>Very Positive: 98% of the 173,878 user reviews for this game are positive.</p>` +
				`</div></article></body></html>`,
			partial: []string{"the page states 173878 reviews; not all are loaded"},
		},
		{
			name: "a store page stating its reviews in microdata",
			page: `<html><body><article>` + articlePreview + `<a class="user_reviews_summary_row" href="#app_reviews_hash" ` +
				`itemprop="aggregateRating" itemscope itemtype="http://schema.org/AggregateRating">` +
				`<span>Very Positive</span><span>- 98% of the 173,878 user reviews for this game are positive.</span>` +
				`<meta itemprop="reviewCount" content="173878"><meta itemprop="ratingValue" content="10"></a>` +
				`</article></body></html>`,
			partial: []string{"the page states 173878 reviews; not all are loaded"},
		},
		{
			name: "a story whose comments load by a Load All Comments link",
			page: `<html><body><article>` + articlePreview + `</article><div id="comments">` +
				`<a href="#" onclick="D2.ajaxFetchComments(0,1,'','',-1); return false" class="btn" id="d2loadall">` +
				`Load All Comments</a></div></body></html>`,
			partial: []string{"the page has a 'Load All Comments' control that was not pressed"},
		},
		{
			name: "a discussion carrying every comment it states",
			page: `<html><head><script type="application/ld+json">[{"@type":"DiscussionForumPosting",` +
				`"interactionStatistic":{"@type":"InteractionCounter","interactionType":"https://schema.org/CommentAction",` +
				`"userInteractionCount":3},"comment":[` + commentItems(3) + `]}]</script></head><body><article>` +
				articlePreview + `</article></body></html>`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			served := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.URL.String() == source {
					return response(request, http.StatusOK, "text/html; charset=UTF-8", tc.page), nil
				}
				return response(request, http.StatusNotFound, "text/html", "<html><body>not found</body></html>"), nil
			})
			converter := &browserSpyConverter{
				convertFn: func(_ context.Context, _, _ string, body []byte) (string, error) {
					text := scriptRe.ReplaceAllString(string(body), " ")
					text = htmlTagRe.ReplaceAllString(styleRe.ReplaceAllString(text, " "), " ")
					return strings.Join(strings.Fields(text), " "), nil
				},
			}
			h := mustNew(t, Options{
				CacheDir:    t.TempDir(),
				Client:      &http.Client{Transport: served},
				Chrome:      &http.Client{Transport: served},
				Jina:        &http.Client{Transport: served},
				OA:          &http.Client{Transport: served},
				Converter:   converter,
				BrowserRung: browserOff(),
				Clock:       newPacingClock(),
			})
			result := h.FetchWithOptions(context.Background(), source, FetchOptions{Refresh: true})
			if result.Error != "" {
				t.Fatalf("the page failed: %s", result.Error)
			}
			for _, want := range tc.partial {
				if !strings.Contains(result.Partial, want) {
					t.Fatalf("partial = %q, want it to name %q", result.Partial, want)
				}
			}
			if len(tc.partial) == 0 && result.Partial != "" {
				t.Fatalf("a page with no thread gap in its markup named %q", result.Partial)
			}
			if !strings.Contains(result.Content, "opening a new front") {
				t.Fatalf("the page's own content was dropped:\n%.600s", result.Content)
			}
		})
	}
}
