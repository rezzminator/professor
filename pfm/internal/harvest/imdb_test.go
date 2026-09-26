package harvest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// The fixtures are two pages of IMDb's GraphQL review list (testdata/imdb) as
// api.graphql.imdb.com answered the TitleReviews query, trimmed and scrubbed:
// the title an invented one, the reviews cut to four across two cursor pages,
// the reviewers' nicknames and ids placeholders, the headlines and texts
// placeholder text, one review with no rating and one flagged a spoiler, as
// the API serves them. The review page itself is served as every anonymous
// rung receives it: the AWS WAF challenge, HTTP 202 with an empty body.
const (
	imdbPageHost = "www.imdb.com"
	imdbURL      = "https://" + imdbPageHost + "/title/tt9000001/reviews/"
)

type imdbSite struct {
	mu       sync.Mutex
	answers  map[string]string // by the request's "after" cursor ("" for the first page)
	status   map[string]int
	requests []string
	headers  []http.Header
}

func newIMDbSite(t *testing.T) *imdbSite {
	return &imdbSite{
		answers: map[string]string{
			"":                          socialFixture(t, "imdb/reviews-1.json"),
			"g4x77placeholdercursorone": socialFixture(t, "imdb/reviews-2.json"),
		},
		status: map[string]int{},
	}
}

func (site *imdbSite) roundTrip(request *http.Request) (*http.Response, error) {
	switch request.URL.Host {
	case imdbPageHost:
		// The WAF challenge every anonymous rung is served in the page's place.
		answer := response(request, http.StatusAccepted, "text/html; charset=UTF-8", "")
		answer.Header.Set("x-amzn-waf-action", "challenge")
		return answer, nil
	case imdbAPIHost:
	default:
		return response(request, http.StatusNotFound, "text/html", "<html><body>not found</body></html>"), nil
	}
	raw, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	var ask struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	if json.Unmarshal(raw, &ask) != nil || request.Method != http.MethodPost ||
		request.Header.Get("x-imdb-client-name") == "" {
		// The API refuses a request without its web client's name (nginx 403).
		return response(request, http.StatusForbidden, "text/html", "<html><body>403 Forbidden</body></html>"), nil
	}
	after, _ := ask.Variables["after"].(string)
	site.mu.Lock()
	site.requests = append(site.requests, fmt.Sprintf("%v/%v/%s", ask.Variables["id"], ask.Variables["first"], after))
	site.headers = append(site.headers, request.Header.Clone())
	site.mu.Unlock()
	if status := site.status[after]; status != 0 {
		return response(request, status, "text/html", "<html><body>refused</body></html>"), nil
	}
	answer, ok := site.answers[after]
	if !ok {
		return response(request, http.StatusOK, "application/json",
			`{"errors":[{"message":"BAD_USER_INPUT exception code while fetching data"}],"data":{"title":null}}`), nil
	}
	return response(request, http.StatusOK, "application/json", answer), nil
}

func (site *imdbSite) harvester(t *testing.T) *Harvester {
	t.Helper()
	h := (&socialSite{}).harvester(t)
	h.client = &http.Client{Transport: roundTripFunc(site.roundTrip)}
	h.chrome = h.client
	h.options.Client, h.options.Chrome = h.client, h.client
	return h
}

// TestIMDbReviewsLoadEveryReview: a title's review page, walled on every
// anonymous rung, renders its user reviews from IMDb's GraphQL API, page after
// page by the cursor each answer names, each request carrying the site's web
// client name and no credential; every review renders once with its headline,
// rating, spoiler flag, author as shown, date and text; the API's total is the
// stated count; the artifact is complete, and a second harvest is identical.
func TestIMDbReviewsLoadEveryReview(t *testing.T) {
	site := newIMDbSite(t)
	h := site.harvester(t)
	result := h.FetchWithOptions(context.Background(), imdbURL, FetchOptions{Refresh: true})
	if result.Error != "" || result.Partial != "" {
		t.Fatalf("the reviews are not complete: partial=%q error=%q\n%.2500s", result.Partial, result.Error,
			result.Content)
	}
	want := "rw9000001,rw9000002,rw9000003,rw9000004"
	if got := strings.Join(socialRendered(result.Content), ","); got != want {
		t.Fatalf("reviews or their order are wrong: got %v, want %v\n%.2500s", got, want, result.Content)
	}
	for _, text := range []string{
		"# An Example Film — user reviews",
		"**Post:** " + imdbURL,
		"**Replies:** 4 stated · 4 loaded",
		"- **Reviewer_One** · 2021-02-17 · [rw9000001](https://www.imdb.com/review/rw9000001/)",
		"**A placeholder headline.**",
		"**Rating:** 10/10",
		"A second placeholder paragraph, 10/10.",
		"**Rating:** none given · **Contains spoilers**",
		"Placeholder text that gives the ending away.",
	} {
		if !strings.Contains(result.Content, text) {
			t.Fatalf("the artifact lacks %q:\n%.2500s", text, result.Content)
		}
	}
	if got := fmt.Sprint(site.requests); got != "[tt9000001/25/ tt9000001/25/g4x77placeholdercursorone]" {
		t.Fatalf("review list requests %v, want the two cursor pages of 25", got)
	}
	for _, header := range site.headers {
		if header.Get("Authorization") != "" || header.Get("Cookie") != "" {
			t.Fatalf("a review request carried a credential: %v", header)
		}
		if header.Get("x-imdb-client-name") != imdbClientName {
			t.Fatalf("a review request lacks the site's web client name: %v", header)
		}
	}
	again := h.FetchWithOptions(context.Background(), imdbURL, FetchOptions{Refresh: true})
	if again.Content != result.Content {
		t.Fatalf("a second harvest differs:\n%.1500s\n---\n%.1500s", result.Content, again.Content)
	}
}

// TestIMDbReviewsNotServedNameTheGap: when the API's list ends before its
// stated total, the rest are named with the count, and the artifact is
// partial; a review page that fails to load is named as well.
func TestIMDbReviewsNotServedNameTheGap(t *testing.T) {
	site := newIMDbSite(t)
	site.answers[""] = strings.Replace(site.answers[""], `"total":4`, `"total":12037`, 1)
	h := site.harvester(t)
	result := h.FetchWithOptions(context.Background(), imdbURL, FetchOptions{Refresh: true})
	for _, text := range []string{
		"**Replies:** 12037 stated · 4 loaded",
		"12033 stated repl(ies) not served: IMDb's GraphQL API ended its review list",
	} {
		if !strings.Contains(result.Content, text) {
			t.Fatalf("the artifact lacks %q:\n%.2500s", text, result.Content)
		}
	}
	if !strings.Contains(result.Partial, "imdb reviews: 4 of 12037 stated replies loaded") {
		t.Fatalf("the artifact is not partial with the gap named: %q", result.Partial)
	}

	refused := newIMDbSite(t)
	refused.status["g4x77placeholdercursorone"] = http.StatusServiceUnavailable
	result = refused.harvester(t).FetchWithOptions(context.Background(), imdbURL, FetchOptions{Refresh: true})
	if !strings.Contains(result.Content, "**Replies:** 4 stated · 2 loaded") ||
		!strings.Contains(result.Partial, "2 stated repl(ies) not served: a further review page of IMDb's "+
			"GraphQL API was not loaded") || !strings.Contains(result.Partial, "HTTP 503") {
		t.Fatalf("a failed review page is not named: partial=%q\n%.2500s", result.Partial, result.Content)
	}
}

// TestIMDbReviewsUnloadedFallThroughNamed: when the API refuses the first
// page, the page is not rendered from nothing: it falls through to the reader
// rung's copy (the few featured reviews, as the reader serves it live), and
// the artifact is partial with the unread review list named.
func TestIMDbReviewsUnloadedFallThroughNamed(t *testing.T) {
	site := newIMDbSite(t)
	site.status[""] = http.StatusForbidden
	h := site.harvester(t)
	featured := "Title: An Example Film\n\nURL Source: " + imdbURL + "\n\nMarkdown Content:\n## An Example Film\n\n" +
		"Featured reviews\n\n#### [A placeholder featured headline](" + imdbURL + "?featured=rw9000001)\n\n" +
		strings.Repeat("A placeholder featured review sentence. ", 30)
	h.jina = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusOK, "text/plain; charset=utf-8", featured), nil
	})}
	result := h.FetchWithOptions(context.Background(), imdbURL, FetchOptions{Refresh: true})
	if result.Error != "" || !strings.Contains(result.Content, "A placeholder featured headline") ||
		strings.Contains(result.Content, "**Replies:**") {
		t.Fatalf("the page did not fall through to the reader's copy: error=%q\n%.1500s", result.Error,
			result.Content)
	}
	if !strings.Contains(result.Partial, "imdb reviews: the title's review list was not loaded") {
		t.Fatalf("the unloaded review list is not named: partial=%q", result.Partial)
	}
}
