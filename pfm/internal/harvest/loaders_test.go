package harvest

import (
	"context"
	"encoding/json"
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

// pacingClock is the wall clock whose Sleep returns at once and records the
// pause asked for: the loader pacing is observable without being waited out.
type pacingClock struct {
	clock.Clock
	mu     sync.Mutex
	sleeps []time.Duration
	// stepping, when set, makes the clock fake: Now is at, which a Sleep and
	// an answer (advance) move on.
	stepping bool
	at       time.Time
}

func newPacingClock() *pacingClock { return &pacingClock{Clock: clock.Real} }

func (c *pacingClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.stepping {
		return c.Clock.Now()
	}
	return c.at
}

// advance moves a stepping clock on by d: the time an answer took.
func (c *pacingClock) advance(d time.Duration) {
	c.mu.Lock()
	c.at = c.at.Add(d)
	c.mu.Unlock()
}

func (c *pacingClock) Sleep(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	c.sleeps = append(c.sleeps, d)
	c.at = c.at.Add(d)
	c.mu.Unlock()
	return ctx.Err()
}

// TestLoaderFollowingStopsAtARateLimit: the site answers 429 to one
// loader request, and again to its one retry. Following ends there — no later
// loader is requested, and no browser render is spent working around it —
// and the artifact is flagged partial naming the rate limit and the gaps.
func TestLoaderFollowingStopsAtARateLimit(t *testing.T) {
	site := walkedThread()
	site.status = map[string]int{"c5": http.StatusTooManyRequests}
	spy := &browserSpyConverter{html: site.page, status: http.StatusOK}
	h, _ := site.harvester(t, spy, browserOn())
	result := h.Fetch(context.Background(), loaderThread)
	if result.Error != "" || result.Method != rungDirect || spy.browserCalls != 0 {
		t.Fatalf("a rate-limited thread was not kept at the direct rung: method=%q rungs=%v browser=%d error=%q",
			result.Method, result.Rungs, spy.browserCalls, result.Error)
	}
	if len(site.requests) != 4 {
		t.Fatalf("%d requests after a 429, want 4 (page, one loader, the rate-limited one and its retry): %+v",
			len(site.requests), site.requests)
	}
	for _, want := range []string{
		"HTTP 429",
		"not retried",
		`"Continue this thread" link(s) not followed`,
		`1 unexpanded "View more comments"`,
	} {
		if !strings.Contains(result.Partial, want) {
			t.Fatalf("the partial marker lacks %q: %q", want, result.Partial)
		}
	}
}

// TestLoaderFollowingStopsAtTheCap: a Reddit thread with one loader more
// than Reddit's own cap, past the default one. Every loader up to Reddit's
// cap is requested, each paced, the last stays a named gap, and the cap is
// named as the reason. (The default cap: the Discourse test.)
func TestLoaderFollowingStopsAtTheCap(t *testing.T) {
	site := &redditSite{fragments: map[string]string{}}
	var tree []string
	for index := range redditLoaderCap + 1 {
		cursor := fmt.Sprintf("cur-%d", index)
		tree = append(tree, threadComment(fmt.Sprintf("p%d", index), "poster_placeholder", "A top comment.",
			moreRepliesLoader(cursor, 1)))
		site.fragments[cursor] = threadComment(fmt.Sprintf("r%d", index), "replier_placeholder", "A reply.")
	}
	site.page = loaderThreadPage(2*(redditLoaderCap+1), tree...)
	spy := &browserSpyConverter{html: site.page, status: http.StatusOK}
	h, pacing := site.harvester(t, spy, browserOn())
	result := h.Fetch(context.Background(), loaderThread)
	if len(pacing.sleeps) != redditLoaderCap || pacing.sleeps[0] != loaderPace {
		t.Fatalf("paced %d times for %d loader requests, want each paced %v", len(pacing.sleeps),
			redditLoaderCap, loaderPace)
	}
	if result.Error != "" || result.Method != rungDirect || spy.browserCalls != 0 {
		t.Fatalf("a capped thread was not kept at the direct rung: method=%q rungs=%v browser=%d error=%q",
			result.Method, result.Rungs, spy.browserCalls, result.Error)
	}
	if loaderRequests := len(site.requests) - 1; loaderRequests != redditLoaderCap {
		t.Fatalf("%d loader requests, want the cap of %d", loaderRequests, redditLoaderCap)
	}
	for _, want := range []string{
		fmt.Sprintf("the cap of %d loader requests", redditLoaderCap),
		`1 behind 1 unexpanded "more replies"`,
		fmt.Sprintf("%d of %d comments loaded", 2*redditLoaderCap+1, 2*(redditLoaderCap+1)),
	} {
		if !strings.Contains(result.Partial, want) {
			t.Fatalf("the partial marker lacks %q: %q", want, result.Partial)
		}
	}
}

// TestLoaderFollowingNamesFailures: every loader request fails. The
// following gives up after three in a row, never requests a loader pointing
// off the site, and names both in the partial marker.
func TestLoaderFollowingNamesFailures(t *testing.T) {
	site := &redditSite{
		page: loaderThreadPage(10,
			threadComment("c1", "alpha_placeholder", "Alpha.",
				`<faceplate-partial method="post" src="https://elsewhere.example.test/svc/shreddit/more-comments/x">`+
					`<input type="hidden" name="cursor" value="cur-off"><button>1 more reply</button>`+
					`</faceplate-partial>`),
			threadComment("c2", "bravo_placeholder", "Bravo.", moreRepliesLoader("cur-1", 2)),
			threadComment("c3", "charlie_placeholder", "Charlie.", moreRepliesLoader("cur-2", 2)),
			threadComment("c4", "delta_placeholder", "Delta.", moreRepliesLoader("cur-3", 2)),
			threadComment("c5", "echo_placeholder", "Echo.", moreRepliesLoader("cur-4", 2)),
		),
		status: map[string]int{"cur-1": 500, "cur-2": 500, "cur-3": 500, "cur-4": 500},
	}
	h, _ := site.harvester(t, &browserSpyConverter{}, browserOff())
	result := h.Fetch(context.Background(), loaderThread)
	if result.Error != "" || result.Method != rungDirect {
		t.Fatalf("thread not kept: method=%q error=%q", result.Method, result.Error)
	}
	for _, request := range site.requests {
		if request.cursor == "cur-off" {
			t.Fatalf("a loader pointing off the site was requested: %+v", request)
		}
	}
	if loaderRequests := len(site.requests) - 1; loaderRequests != 3 {
		t.Fatalf("%d loader requests, want 3 before giving up: %+v", loaderRequests, site.requests)
	}
	for _, want := range []string{
		"points off the site",
		"HTTP 500",
		"3 loader requests in a row failed",
		`9 behind 5 unexpanded "more replies"`,
	} {
		if !strings.Contains(result.Partial, want) {
			t.Fatalf("the partial marker lacks %q: %q", want, result.Partial)
		}
	}
}

// TestLoaderFollowingStopsAtABotWall: the site answers one loader with
// its bot wall (HTTP 200, no comments). Following ends there, the wall is
// never retried, and the partial marker names it; a comment merely quoting a
// wall's phrase is still grafted.
func TestLoaderFollowingStopsAtABotWall(t *testing.T) {
	site := walkedThread()
	site.walls = map[string]string{"cur-top": `<html><head><title>Reddit - Prove your humanity</title></head>` +
		`<body><h1>Prove your humanity</h1></body></html>`}
	h, _ := site.harvester(t, &browserSpyConverter{}, browserOff())
	result := h.Fetch(context.Background(), loaderThread)
	if result.Error != "" || result.Method != rungDirect {
		t.Fatalf("a walled thread was not kept: method=%q error=%q", result.Method, result.Error)
	}
	if len(site.requests) != 4 {
		t.Fatalf("%d requests, want 4 (page, two loaders, the walled one): %+v", len(site.requests), site.requests)
	}
	for _, want := range []string{"bot wall", `titled "Reddit - Prove your humanity"`, "not retried"} {
		if !strings.Contains(result.Partial, want) {
			t.Fatalf("the partial marker lacks %q: %q", want, result.Partial)
		}
	}
	if !strings.Contains(result.Content, "Just a moment: checking your browser settings fixed it.") {
		t.Fatalf("a comment quoting a wall phrase was not grafted: %.900q", result.Content)
	}
}

// TestLoaderGraftFailuresAreBoundedNotRawErrorText pins F5: a loader graft
// failure's PUBLIC class stays bounded, so a page whose title is
// arbitrarily long — or any future extractor's graft wrapping something
// longer than expected — cannot leak past graftErrorReasonMaxLen into the
// partial marker, the PARTIAL receipt or the cache. An ordinary short title
// (TestLoaderFollowingStopsAtABotWall's ""Reddit - Prove your humanity"")
// still passes through whole; only a pathologically long one is capped.
func TestLoaderGraftFailuresAreBoundedNotRawErrorText(t *testing.T) {
	longTitle := "Reddit - " + strings.Repeat("x", 400)
	site := walkedThread()
	site.walls = map[string]string{"cur-top": `<html><head><title>` + longTitle + `</title></head>` +
		`<body><h1>` + longTitle + `</h1></body></html>`}
	h, _ := site.harvester(t, &browserSpyConverter{}, browserOff())
	result := h.Fetch(context.Background(), loaderThread)
	if result.Error != "" || result.Method != rungDirect {
		t.Fatalf("a walled thread was not kept: method=%q error=%q", result.Method, result.Error)
	}
	if strings.Contains(result.Partial, longTitle) {
		t.Fatalf("the full %d-char page title leaked into the partial marker unbounded: %.400q",
			len(longTitle), result.Partial)
	}
	// A generous bound well under the 400 'x's the title carries and clear of
	// the production 200-char cap either side of a rename or retune.
	const wantBoundedUnder = 300
	if got := strings.Count(result.Partial, "x"); got > wantBoundedUnder {
		t.Fatalf("the partial marker repeats %d characters of the page title, over the %d-char bound: %.400q",
			got, wantBoundedUnder, result.Partial)
	}
}

// TestABackoffOnAnErrorAnswerDelaysTheNextRequest: a back-off the site asks
// for on an answer that failed is honoured like one on an answer kept — the
// next request waits it out, not the plain pace.
func TestABackoffOnAnErrorAnswerDelaysTheNextRequest(t *testing.T) {
	const host = "api.example.test"
	site := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/refused" {
			return response(request, http.StatusInternalServerError, "application/json", `{"backoff": 7}`), nil
		}
		return response(request, http.StatusOK, "application/json", `{}`), nil
	})
	pacing := newPacingClock()
	h := mustNew(t, Options{
		CacheDir:  t.TempDir(),
		Client:    &http.Client{Transport: site},
		Converter: &browserSpyConverter{},
		Clock:     pacing,
	})
	gone := map[string]bool{}
	loader := func(path string) pageLoader {
		return pageLoader{
			key: path, label: path, method: http.MethodGet, target: "https://" + host + path,
			graft: func([]byte, string) error { gone[path] = true; return nil },
			drop:  func() { gone[path] = true },
			backoff: func(body []byte) time.Duration {
				var answer struct {
					Backoff int `json:"backoff"`
				}
				if err := json.Unmarshal(body, &answer); err != nil {
					t.Errorf("the answer is not JSON: %v", err)
				}
				return time.Duration(answer.Backoff) * time.Second
			},
		}
	}
	extractor := siteExtractor{
		name:  "backoff-test",
		hosts: []string{host},
		loaders: func(*html.Node, *url.URL) []pageLoader {
			var left []pageLoader
			for _, path := range []string{"/refused", "/next"} {
				if !gone[path] {
					left = append(left, loader(path))
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
	h.followLoaders(context.Background(), doc, page, extractor, newLoaderBudget(context.Background()))
	want := []time.Duration{loaderPace, 7 * time.Second}
	if fmt.Sprint(pacing.sleeps) != fmt.Sprint(want) {
		t.Fatalf("requests waited %v, want %v", pacing.sleeps, want)
	}
}
