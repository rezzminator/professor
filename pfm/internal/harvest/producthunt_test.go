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

// The fixtures are a captured Product Hunt reviews page and two pages of the
// site's GraphQL review list (testdata/producthunt), trimmed and scrubbed: the
// product an invented one, the page cut to its header, summary and tabs and
// one ApolloSSRDataTransport script keeping the transport's shape (the
// JavaScript `undefined` values, the DetailedReviews answer stating the
// product's count, the DetailedReviewsPage answer embedding the first
// reviews, its list total two past the page's count, as the site serves
// it); the reviewers' names and handles placeholders, the texts placeholder
// HTML of the captured shape.
const (
	productHuntHost = "www.producthunt.com"
	productHuntURL  = "https://" + productHuntHost + "/products/example-app/reviews"
)

type productHuntSite struct {
	mu       sync.Mutex
	page     string
	answers  map[int]string
	status   map[int]int
	requests []int
	bodies   []map[string]any
	headers  []http.Header
}

func newProductHuntSite(t *testing.T) *productHuntSite {
	return &productHuntSite{
		page: socialFixture(t, "producthunt/reviews-page.html"),
		answers: map[int]string{
			1: socialFixture(t, "producthunt/reviews-1.json"), 2: socialFixture(t, "producthunt/reviews-2.json"),
		},
		status: map[int]int{},
	}
}

func (site *productHuntSite) roundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Host != productHuntHost {
		return response(request, http.StatusNotFound, "text/html", "<html><body>not found</body></html>"), nil
	}
	if request.URL.Path != "/frontend/graphql" {
		return response(request, http.StatusOK, "text/html; charset=utf-8", site.page), nil
	}
	raw, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	var ask struct {
		Variables  map[string]any `json:"variables"`
		Query      string         `json:"query"`
		Extensions struct {
			Persisted struct {
				Hash string `json:"sha256Hash"`
			} `json:"persistedQuery"`
		} `json:"extensions"`
	}
	if json.Unmarshal(raw, &ask) != nil || ask.Extensions.Persisted.Hash == "" {
		// The site refuses a query sent without its persisted-query hash.
		return response(request, http.StatusOK, "application/json",
			`{"errors":[{"message":"This request could not be processed"}]}`), nil
	}
	page, _ := ask.Variables["reviewsPage"].(float64)
	site.mu.Lock()
	site.requests = append(site.requests, int(page))
	site.bodies = append(site.bodies, ask.Variables)
	site.headers = append(site.headers, request.Header.Clone())
	site.mu.Unlock()
	if status := site.status[int(page)]; status != 0 {
		return response(request, status, "text/html", "<html><body>refused</body></html>"), nil
	}
	answer, ok := site.answers[int(page)]
	if !ok {
		return response(request, http.StatusOK, "application/json", `{"data":{"product":null}}`), nil
	}
	return response(request, http.StatusOK, "application/json; charset=utf-8", answer), nil
}

func (site *productHuntSite) harvester(t *testing.T) *Harvester {
	t.Helper()
	social := &socialSite{}
	h := social.harvester(t)
	h.client = &http.Client{Transport: roundTripFunc(site.roundTrip)}
	h.options.Client, h.options.Chrome = h.client, h.client
	return h
}

// TestProductHuntReviewsLoadEveryReview: a Product Hunt reviews page renders
// its reviews from the site's GraphQL review list on the page's own host,
// page after page, each request carrying the persisted-query hash and no
// credential; every review renders once with its author as shown, rating,
// date and written parts; the list's total is the stated count and the
// page's own count is named beside it; the artifact is complete, and a
// second harvest is identical.
func TestProductHuntReviewsLoadEveryReview(t *testing.T) {
	site := newProductHuntSite(t)
	h := site.harvester(t)
	result := h.FetchWithOptions(context.Background(), productHuntURL, FetchOptions{Refresh: true})
	if result.Error != "" || result.Partial != "" {
		t.Fatalf("the reviews are not complete: partial=%q error=%q\n%.2500s", result.Partial, result.Error,
			result.Content)
	}
	want := "700005,700004,700003,700002,700001"
	if got := strings.Join(socialRendered(result.Content), ","); got != want {
		t.Fatalf("reviews or their order are wrong: got %v, want %v\n%.2500s", got, want, result.Content)
	}
	for _, text := range []string{
		"# Example App reviews",
		"**Replies:** 5 stated · 5 loaded",
		"The page states 4 reviews; the site's review list counts 5.",
		"- **Reviewer Four (@reviewer_four)** · 2026-08-18 16:57 UTC · [700004](" + productHuntURL + "?review=700004)",
		"**4/5** · personal review",
		"**What could be better:**",
		"Performance is probably the biggest weakness.",
		"a quoted \"undefined\" stays text.",
		"**Placeholder Notes**",
		"**Overall:**",
	} {
		if !strings.Contains(result.Content, text) {
			t.Fatalf("the artifact lacks %q:\n%.2500s", text, result.Content)
		}
	}
	if fmt.Sprint(site.requests) != "[1 2]" {
		t.Fatalf("review list requests %v, want pages 1 and 2", site.requests)
	}
	for index, header := range site.headers {
		if header.Get("Authorization") != "" || header.Get("Cookie") != "" {
			t.Fatalf("a review request carried a credential: %v", header)
		}
		if header.Get("X-Requested-With") != "XMLHttpRequest" || site.bodies[index]["slug"] != "example-app" {
			t.Fatalf("a review request is not the site client's: %v %v", header, site.bodies[index])
		}
	}
	again := h.FetchWithOptions(context.Background(), productHuntURL, FetchOptions{Refresh: true})
	if again.Content != result.Content {
		t.Fatal("a second harvest of the same reviews differs")
	}
}

// TestProductHuntReviewsBeyondTheBoundAreNamed: a product stating more
// reviews than the bounded page count reads renders the newest pages and
// names the rest, stated · loaded, in the partial marker.
func TestProductHuntReviewsBeyondTheBoundAreNamed(t *testing.T) {
	site := newProductHuntSite(t)
	first := strings.Replace(site.answers[1], `"totalCount":5`, `"totalCount":90000`, 1)
	for page := 1; page <= productHuntReviewPages+1; page++ {
		site.answers[page] = strings.ReplaceAll(first, `"id":"70000`, fmt.Sprintf(`"id":"%d0000`, 70+page))
	}
	result := site.harvester(t).FetchWithOptions(context.Background(), productHuntURL, FetchOptions{Refresh: true})
	loaded := 3 * productHuntReviewPages
	if result.Error != "" || len(socialRendered(result.Content)) != loaded {
		t.Fatalf("the loaded reviews were not rendered: error=%q\n%.2500s", result.Error, result.Content)
	}
	if len(site.requests) != productHuntReviewPages {
		t.Fatalf("review list requests %v, want %d", site.requests, productHuntReviewPages)
	}
	for _, want := range []string{
		fmt.Sprintf("product hunt reviews: %d of 90000 stated replies loaded", loaded),
		fmt.Sprintf("%d stated repl(ies) not served: the harvester reads the newest", 90000-loaded),
	} {
		if !strings.Contains(result.Partial, want) {
			t.Fatalf("the partial marker lacks %q: %q", want, result.Partial)
		}
	}
}

// TestProductHuntReviewsNotLoadedNameTheGap: a review list the site refuses,
// or answers with a GraphQL error, is never rendered: the page's embedded
// reviews render and the rest are named against the list's total, never a
// product with only those reviews.
func TestProductHuntReviewsNotLoadedNameTheGap(t *testing.T) {
	for name, mutate := range map[string]func(site *productHuntSite){
		"refused": func(site *productHuntSite) { site.status[1] = http.StatusForbidden },
		"graphql error": func(site *productHuntSite) {
			site.answers[1] = `{"errors":[{"message":"Query has complexity of 521538.0"}]}`
		},
	} {
		t.Run(name, func(t *testing.T) {
			site := newProductHuntSite(t)
			mutate(site)
			result := site.harvester(t).FetchWithOptions(context.Background(), productHuntURL,
				FetchOptions{Refresh: true})
			for _, want := range []string{
				"product hunt reviews: 2 of 5 stated replies loaded",
				"1 loader request(s) failed (the product's review page 1 (https://" + productHuntHost +
					"/frontend/graphql): ",
				"3 stated repl(ies) not served: the page embeds only its first reviews",
			} {
				if !strings.Contains(result.Partial, want) {
					t.Fatalf("the partial marker lacks %q: %q (error %q)\n%.1500s", want, result.Partial,
						result.Error, result.Content)
				}
			}
			if got := strings.Join(socialRendered(result.Content), ","); got != "700005,700004" {
				t.Fatalf("rendered %v, want the two embedded reviews", got)
			}
		})
	}
}

// TestProductHuntPageWithoutStateFallsThrough: a reviews address whose page
// holds no embedded product state is not claimed: it keeps the generic path.
func TestProductHuntPageWithoutStateFallsThrough(t *testing.T) {
	site := newProductHuntSite(t)
	site.page = strings.Replace(site.page, "ApolloSSRDataTransport", "SomeOtherTransport", 1)
	result := site.harvester(t).FetchWithOptions(context.Background(), productHuntURL, FetchOptions{Refresh: true})
	if strings.Contains(result.Content, "**Replies:**") || len(site.requests) != 0 {
		t.Fatalf("a page with no embedded state was claimed: requests %v\n%.1500s", site.requests, result.Content)
	}
}
