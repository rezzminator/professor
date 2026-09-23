package harvest

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/html"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

// rateLimitAnswer is one answer of a rate-limit test site: a status and a
// Retry-After ("" for none).
type rateLimitAnswer struct {
	status     int
	retryAfter string
}

// followRateLimited follows one loader per path on api.example.test, in
// order; answers[path] lists the answer to each request of that path in turn,
// the last one repeating (none: 200). It returns the requests sent and the
// budget's stop; the waits are on clk.
func followRateLimited(
	t *testing.T,
	clk clock.Clock,
	paths []string,
	answers map[string][]rateLimitAnswer,
) (requests []string, stopped string) {
	t.Helper()
	const host = "api.example.test"
	var mu sync.Mutex
	seen := map[string]int{}
	site := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		path := request.URL.Path
		requests = append(requests, path)
		list := answers[path]
		if len(list) == 0 {
			return response(request, http.StatusOK, "application/json", `{}`), nil
		}
		answer := list[min(seen[path], len(list)-1)]
		seen[path]++
		reply := response(request, answer.status, "application/json", `{}`)
		if answer.retryAfter != "" {
			reply.Header.Set("Retry-After", answer.retryAfter)
		}
		return reply, nil
	})
	h := mustNew(t, Options{
		CacheDir:  t.TempDir(),
		Client:    &http.Client{Transport: site},
		Converter: &browserSpyConverter{},
		Clock:     clk,
	})
	gone := map[string]bool{}
	extractor := siteExtractor{
		name:  "rate-limit-test",
		hosts: []string{host},
		loaders: func(*html.Node, *url.URL) []pageLoader {
			var left []pageLoader
			for _, path := range paths {
				if !gone[path] {
					left = append(left, pageLoader{
						key: path, label: path, method: http.MethodGet, target: "https://" + host + path,
						graft: func([]byte, string) error { gone[path] = true; return nil },
						drop:  func() { gone[path] = true },
					})
				}
			}
			return left
		},
	}
	doc, err := html.Parse(strings.NewReader("<html><body></body></html>"))
	if err != nil {
		t.Fatal(err)
	}
	page, err := url.Parse("https://" + host + "/thread")
	if err != nil {
		t.Fatal(err)
	}
	budget := newLoaderBudget(context.Background())
	h.followLoaders(context.Background(), doc, page, extractor, budget)
	return requests, budget.stopped
}

// TestALoaderRateLimitIsWaitedOutAndRetriedOnce: a 429 is waited out for the
// server's Retry-After (a default when it gave none) and retried once; a wait
// past the one-wait cap or the fetch's rate-limit budget is never waited, and
// the stop names the server's wait; a second 429 on the retry ends the
// following, naming the retry.
func TestALoaderRateLimitIsWaitedOutAndRetriedOnce(t *testing.T) {
	limited := func(retryAfter string) rateLimitAnswer {
		return rateLimitAnswer{status: http.StatusTooManyRequests, retryAfter: retryAfter}
	}
	ok := rateLimitAnswer{status: http.StatusOK}
	cases := []struct {
		name         string
		paths        []string
		answers      map[string][]rateLimitAnswer
		wantSleeps   []time.Duration
		wantRequests []string
		wantStop     []string
	}{
		{
			name:         "within the cap: waited, retried, the next page loads",
			paths:        []string{"/a", "/b"},
			answers:      map[string][]rateLimitAnswer{"/a": {limited("5"), ok}},
			wantSleeps:   []time.Duration{loaderPace, 5 * time.Second, loaderPace},
			wantRequests: []string{"/a", "/a", "/b"},
		},
		{
			name:         "no Retry-After: the default wait",
			paths:        []string{"/a", "/b"},
			answers:      map[string][]rateLimitAnswer{"/a": {limited(""), ok}},
			wantSleeps:   []time.Duration{loaderPace, 10 * time.Second, loaderPace},
			wantRequests: []string{"/a", "/a", "/b"},
		},
		{
			name:         "past the one-wait cap: not waited, the server's wait named",
			paths:        []string{"/a", "/b"},
			answers:      map[string][]rateLimitAnswer{"/a": {limited("3600")}},
			wantSleeps:   []time.Duration{loaderPace},
			wantRequests: []string{"/a"},
			wantStop:     []string{"HTTP 429", "retry after 3600 s", "past the 60 s one wait may take", "not retried"},
		},
		{
			name:  "the fetch's budget holds across repeated 429s",
			paths: []string{"/a", "/b", "/c"},
			answers: map[string][]rateLimitAnswer{
				"/a": {limited("50"), ok}, "/b": {limited("50"), ok}, "/c": {limited("50"), ok},
			},
			wantSleeps: []time.Duration{
				loaderPace, 50 * time.Second, loaderPace, 50 * time.Second, loaderPace,
			},
			wantRequests: []string{"/a", "/a", "/b", "/b", "/c"},
			wantStop:     []string{"HTTP 429", "retry after 50 s", "the 20 s left of the 120 s", "not retried"},
		},
		{
			name:         "a second 429 on the retry ends the following",
			paths:        []string{"/a", "/b"},
			answers:      map[string][]rateLimitAnswer{"/a": {limited("5")}},
			wantSleeps:   []time.Duration{loaderPace, 5 * time.Second},
			wantRequests: []string{"/a", "/a"},
			wantStop:     []string{"HTTP 429", "again after a 5 s wait and one retry", "not retried again"},
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

// cancellingClock cancels the fetch when asked for a wait past the pace: the
// fetch is cancelled while a rate limit is waited out.
type cancellingClock struct {
	*pacingClock
	cancel context.CancelFunc
}

func (c *cancellingClock) Sleep(ctx context.Context, d time.Duration) error {
	if d > loaderPace {
		c.cancel()
	}
	return c.pacingClock.Sleep(ctx, d)
}

// TestALoaderRateLimitWaitEndsWithTheFetch: a fetch cancelled during the
// rate-limit wait sends no retry and names the cancellation.
func TestALoaderRateLimitWaitEndsWithTheFetch(t *testing.T) {
	const host = "api.example.test"
	var requests []string
	site := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests = append(requests, request.URL.Path)
		reply := response(request, http.StatusTooManyRequests, "application/json", `{}`)
		reply.Header.Set("Retry-After", "5")
		return reply, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := mustNew(t, Options{
		CacheDir:  t.TempDir(),
		Client:    &http.Client{Transport: site},
		Converter: &browserSpyConverter{},
		Clock:     &cancellingClock{pacingClock: newPacingClock(), cancel: cancel},
	})
	gone := false
	extractor := siteExtractor{
		name:  "rate-limit-cancel-test",
		hosts: []string{host},
		loaders: func(*html.Node, *url.URL) []pageLoader {
			if gone {
				return nil
			}
			return []pageLoader{{
				key: "/a", label: "/a", method: http.MethodGet, target: "https://" + host + "/a",
				graft: func([]byte, string) error { gone = true; return nil },
				drop:  func() { gone = true },
			}}
		},
	}
	doc, err := html.Parse(strings.NewReader("<html><body></body></html>"))
	if err != nil {
		t.Fatal(err)
	}
	page, err := url.Parse("https://" + host + "/thread")
	if err != nil {
		t.Fatal(err)
	}
	budget := newLoaderBudget(ctx)
	h.followLoaders(ctx, doc, page, extractor, budget)
	if len(requests) != 1 || !strings.Contains(budget.stopped, "cancelled") {
		t.Fatalf("a fetch cancelled during the wait: requests %v, stop %q", requests, budget.stopped)
	}
}
