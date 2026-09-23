package harvest

import (
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
