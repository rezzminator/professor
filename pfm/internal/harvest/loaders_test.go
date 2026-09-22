package harvest

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

// pacingClock is the wall clock whose Sleep returns at once and records the
// pause asked for: the loader pacing is observable without being waited out.
type pacingClock struct {
	clock.Clock
	mu     sync.Mutex
	sleeps []time.Duration
}

func newPacingClock() *pacingClock { return &pacingClock{Clock: clock.Real} }

func (c *pacingClock) Sleep(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	c.sleeps = append(c.sleeps, d)
	c.mu.Unlock()
	return ctx.Err()
}

// TestLoaderFollowingStopsAtARateLimit: the site answers 429 to one
// loader request. Following ends there — no later loader is requested, the
// 429 is never retried, and no browser render is spent working around it —
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
	if len(site.requests) != 3 {
		t.Fatalf("%d requests after a 429, want 3 (page, one loader, the rate-limited one): %+v",
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

// TestLoaderFollowingStopsAtTheCap: a thread with one loader more than
// the per-page cap. Exactly the cap is requested, the last loader stays a
// named gap, and the cap is named as the reason.
func TestLoaderFollowingStopsAtTheCap(t *testing.T) {
	site := &redditSite{fragments: map[string]string{}}
	var tree []string
	for index := range loaderRequestCap + 1 {
		cursor := fmt.Sprintf("cur-%d", index)
		tree = append(tree, threadComment(fmt.Sprintf("p%d", index), "poster_placeholder", "A top comment.",
			moreRepliesLoader(cursor, 1)))
		site.fragments[cursor] = threadComment(fmt.Sprintf("r%d", index), "replier_placeholder", "A reply.")
	}
	site.page = loaderThreadPage(2*(loaderRequestCap+1), tree...)
	spy := &browserSpyConverter{html: site.page, status: http.StatusOK}
	h, _ := site.harvester(t, spy, browserOn())
	result := h.Fetch(context.Background(), loaderThread)
	if result.Error != "" || result.Method != rungDirect || spy.browserCalls != 0 {
		t.Fatalf("a capped thread was not kept at the direct rung: method=%q rungs=%v browser=%d error=%q",
			result.Method, result.Rungs, spy.browserCalls, result.Error)
	}
	if loaderRequests := len(site.requests) - 1; loaderRequests != loaderRequestCap {
		t.Fatalf("%d loader requests, want the cap of %d", loaderRequests, loaderRequestCap)
	}
	for _, want := range []string{
		fmt.Sprintf("the cap of %d loader requests", loaderRequestCap),
		`1 behind 1 unexpanded "more replies"`,
		fmt.Sprintf("%d of %d comments loaded", 2*loaderRequestCap+1, 2*(loaderRequestCap+1)),
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
