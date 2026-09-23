package harvest

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"
)

// TestALoaderIsPacedByTheSiteQuota: a site reporting its request quota
// (Reddit's x-ratelimit-* headers, the values as captured from its comment
// loader) paces the following: the pace is kept while the quota is plentiful,
// the last requests are spread over what is left of its window, a spent
// quota is waited out before the next request, and a 429 without Retry-After
// waits for the reset — each only within the one-wait cap and the fetch's
// budget; past them, the stop names when the quota resets.
func TestALoaderIsPacedByTheSiteQuota(t *testing.T) {
	answer := func(status int, used, remaining, reset string) rateLimitAnswer {
		return rateLimitAnswer{status: status, quota: [3]string{used, remaining, reset}}
	}
	cases := []struct {
		name         string
		paths        []string
		answers      map[string][]rateLimitAnswer
		wantSleeps   []time.Duration
		wantRequests []string
		wantStop     []string
	}{
		{
			name:         "a plentiful quota keeps the pace",
			paths:        []string{"/a", "/b"},
			answers:      map[string][]rateLimitAnswer{"/a": {answer(http.StatusOK, "1", "199.0", "330")}},
			wantSleeps:   []time.Duration{loaderPace, loaderPace},
			wantRequests: []string{"/a", "/b"},
		},
		{
			name:  "the last requests are spread over the window",
			paths: []string{"/a", "/b", "/c"},
			answers: map[string][]rateLimitAnswer{
				"/a": {answer(http.StatusOK, "160", "40.0", "120")},
				"/b": {answer(http.StatusOK, "161", "39.0", "117")},
			},
			wantSleeps:   []time.Duration{loaderPace, 3 * time.Second, 3 * time.Second},
			wantRequests: []string{"/a", "/b", "/c"},
		},
		{
			name:         "a spent quota is waited out before the next request",
			paths:        []string{"/a", "/b"},
			answers:      map[string][]rateLimitAnswer{"/a": {answer(http.StatusOK, "200", "0.0", "30")}},
			wantSleeps:   []time.Duration{loaderPace, 31 * time.Second},
			wantRequests: []string{"/a", "/b"},
		},
		{
			name:         "a spent quota resetting past the one-wait cap: not requested, the reset named",
			paths:        []string{"/a", "/b"},
			answers:      map[string][]rateLimitAnswer{"/a": {answer(http.StatusOK, "200", "0.0", "154")}},
			wantSleeps:   []time.Duration{loaderPace},
			wantRequests: []string{"/a"},
			wantStop: []string{
				"request quota spent after 1 request(s)", "resetting in 154 s (at ", " UTC)",
				"past the 60 s one wait may take", "not requested",
			},
		},
		{
			name:  "a 429 without Retry-After waits for the quota's reset",
			paths: []string{"/a", "/b"},
			answers: map[string][]rateLimitAnswer{"/a": {
				answer(http.StatusTooManyRequests, "200", "0.0", "45"), answer(http.StatusOK, "1", "199.0", "600"),
			}},
			wantSleeps:   []time.Duration{loaderPace, 46 * time.Second, loaderPace},
			wantRequests: []string{"/a", "/a", "/b"},
		},
		{
			name:         "a 429 whose quota resets past the one-wait cap: not retried, the reset named",
			paths:        []string{"/a", "/b"},
			answers:      map[string][]rateLimitAnswer{"/a": {answer(http.StatusTooManyRequests, "200", "0.0", "154")}},
			wantSleeps:   []time.Duration{loaderPace},
			wantRequests: []string{"/a"},
			wantStop: []string{
				"HTTP 429", "reported its request quota resets in 154 s (at ", "past the 60 s one wait may take",
				"not retried",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pacing := newPacingClock()
			requests, stopped := followRateLimited(t, pacing, tc.paths, tc.answers)
			if fmt.Sprint(pacing.sleeps) != fmt.Sprint(tc.wantSleeps) {
				t.Errorf("waits %v, want %v", pacing.sleeps, tc.wantSleeps)
			}
			if fmt.Sprint(requests) != fmt.Sprint(tc.wantRequests) {
				t.Errorf("requests %v, want %v", requests, tc.wantRequests)
			}
			if len(tc.wantStop) == 0 && stopped != "" {
				t.Errorf("the following stopped: %q", stopped)
			}
			for _, want := range tc.wantStop {
				if !strings.Contains(stopped, want) {
					t.Errorf("the stop lacks %q: %q", want, stopped)
				}
			}
		})
	}
}

// TestRedditLoadersReadTheQuota: a thread's more-comments loaders opt into
// the quota pacing; its continue links (a page GET, sent no quota) do not.
func TestRedditLoadersReadTheQuota(t *testing.T) {
	doc, err := html.Parse(strings.NewReader(wallFixture(t, "reddit-thread-ssr.html")))
	if err != nil {
		t.Fatal(err)
	}
	page, err := url.Parse("https://www.reddit.com/r/examplesub/comments/abc123/example/")
	if err != nil {
		t.Fatal(err)
	}
	loaders := redditLoaders(doc, page)
	posts := 0
	for _, loader := range loaders {
		if loader.method == http.MethodPost {
			posts++
			if !loader.quotaHeaders {
				t.Errorf("%s is not paced by Reddit's quota", loader.label)
			}
		}
	}
	if posts == 0 {
		t.Fatalf("the fixture named no more-comments loader: %d loader(s)", len(loaders))
	}
}

// TestRedditPacingStopsAtItsBudgetAndNamesTheRest: a thread whose quota
// would pace the following past loaderPacingBudget (10 requests left of a
// window resetting in 590 s: 59 s a request) while every answer takes 30 s
// stops within the budget measured on the fetch's clock from its first paced
// request — waits and answers together — and the partial names the comments
// loaded of those stated, the quota the headers reported, the minutes the
// rest would take and when the quota resets.
func TestRedditPacingStopsAtItsBudgetAndNamesTheRest(t *testing.T) {
	tree := []string{
		threadComment("c1", "alpha_placeholder", "Alpha top comment."),
		threadComment("c2", "bravo_placeholder", "Bravo top comment."),
		viewMoreLoader("cur-1"),
	}
	fragments := map[string]string{}
	for page := 1; page <= 8; page++ {
		fragments[fmt.Sprintf("cur-%d", page)] = threadComment(fmt.Sprintf("m%d", page), "more_placeholder",
			fmt.Sprintf("Comment %d of the loaded pages.", page)) + viewMoreLoader(fmt.Sprintf("cur-%d", page+1))
	}
	const answer = 30 * time.Second
	site := &redditSite{
		page:      loaderThreadPage(20, tree...),
		fragments: fragments,
		quota:     [3]string{"990", "10.0", "590"},
		answer:    answer,
	}
	h, clock := site.harvester(t, &browserSpyConverter{}, browserOff())
	start := clock.Now()
	result := h.FetchWithOptions(context.Background(), loaderThread, FetchOptions{Refresh: true})
	if result.Error != "" {
		t.Fatalf("the fetch failed: %q", result.Error)
	}
	// The page and the first loader answer before any quota was reported.
	if paced := clock.Now().Sub(start) - 2*answer; paced > loaderPacingBudget {
		t.Errorf("the paced following took %s of waits and answers, past the %s budget: waits %v", paced,
			loaderPacingBudget, clock.sleeps)
	}
	if got := len(site.requests); got != 4 {
		t.Errorf("%d requests, want 4 (the page and the three loaders the budget holds): %+v", got, site.requests)
	}
	want := "5 of 20 comments loaded; Reddit reported a quota of 1000 requests a window (990 used, 10 left, " +
		"resetting at 21:13:49 UTC) and the rest would take about 1 more minute — read it again with refresh " +
		"after 21:13:49 UTC to continue"
	if !strings.Contains(result.Partial, want) {
		t.Errorf("the partial lacks %q: %q", want, result.Partial)
	}
}

// TestPacingLeftFallsBackToTheDocumentedRate: a site that reported no used
// count names its documented rate as such.
func TestPacingLeftFallsBackToTheDocumentedRate(t *testing.T) {
	now := time.Date(2026, 9, 23, 21, 0, 0, 0, time.UTC)
	budget := &loaderBudget{
		requests: 10, pacingStop: true, quotaRate: &redditQuota,
		quota: &loaderQuota{remaining: 3, resetAt: now.Add(90 * time.Second)},
	}
	got := budget.pacingLeft(commentCount{loaded: 100, stated: 1300}, now)
	want := "100 of 1300 comments loaded; the site sent no quota header, and Reddit's documented rate is 200 " +
		"requests per 10 minutes: the rest would take about 6 more minutes — read it again with refresh after " +
		"21:01:30 UTC to continue"
	if got != want {
		t.Errorf("pacingLeft = %q, want %q", got, want)
	}
}
