package harvest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"golang.org/x/net/html"
)

func TestRedditThreadExtractorRendersTheTreeAndNamesEveryGap(t *testing.T) {
	doc, err := html.Parse(strings.NewReader(wallFixture(t, "reddit-thread-rendered.html")))
	if err != nil {
		t.Fatal(err)
	}
	extraction, extractor, ok := extractForSite(
		"https://www.reddit.com/r/examplesub/comments/abc123/placeholder_thread_title/",
		doc,
	)
	if !ok || extractor != "reddit-thread" {
		t.Fatalf("the Reddit extractor did not claim a thread page: ok=%t extractor=%q", ok, extractor)
	}
	md := extraction.markdown
	for _, want := range []string{
		"# Placeholder thread title",
		"**Subreddit:** r/examplesub · **Author:** u/op_placeholder · **Score:** 321 · **Posted:** 2026-09-01",
		"[an example tool](https://example.org/tool)",
		"- Second listed question with `inline_code()`",
		"**Comments:** 12 stated · 8 loaded (1 deleted, 1 removed)",
		`3 behind 1 unexpanded "more replies"`,
		`1 unexpanded "View more comments" loader(s)`,
		`1 "Continue this thread" link(s) not followed`,
		"1 not in the page",
		"- **u/alpha_placeholder** · 101 points · 2026-09-01\n  Top comment alpha, first paragraph.\n\n  Alpha second paragraph with **emphasis**.",
		"  - **u/bravo_placeholder** · 55 points · 2026-09-01\n    Reply bravo under alpha about generics in practice.",
		"    - **u/charlie_placeholder** · 21 points",
		"      > Reply bravo under alpha.",
		"      - **u/delta_placeholder** · 8 points",
		"        ```\n        run --flag value\n        second line\n        ```",
		"  - **[deleted]** · *deleted*",
		"    - **u/echo_placeholder** · 4 points",
		"- **u/golf_placeholder** · *removed*",
		"[subreddit link](https://www.reddit.com/r/othersub/)",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("thread markdown lacks %q:\n%s", want, md)
		}
	}
	for _, unwanted := range []string{"Sponsored placeholder", "Related placeholder post", "Log In", "Reply\n", "force-legacy-sct", "/search/?q="} {
		if strings.Contains(md, unwanted) {
			t.Fatalf("thread markdown carries page chrome %q:\n%s", unwanted, md)
		}
	}
	if !strings.Contains(extraction.partial, "8 of 12 comments loaded") {
		t.Fatalf("an incomplete thread was not flagged partial: %q", extraction.partial)
	}
}

// TestRedditSSRPageEscalatesToTheBrowserRung: the plain GET returns the first
// page of comments (inside a <template>); the extractor flags it partial, so
// the ladder spends the browser rung — skipping the readers, which cannot load
// more — and the fuller browser render wins.
func TestRedditSSRPageEscalatesToTheBrowserRung(t *testing.T) {
	spy := &browserSpyConverter{
		html:      wallFixture(t, "reddit-thread-rendered.html"),
		status:    http.StatusOK,
		convertFn: func(context.Context, string, string, []byte) (string, error) { return "", errors.New("unused") },
	}
	h := pageHarvester(t, wallFixture(t, "reddit-thread-ssr.html"), spy, browserOn())
	result := h.Fetch(
		context.Background(),
		"https://www.reddit.com/r/examplesub/comments/abc123/placeholder_thread_title/",
	)
	if result.Error != "" || result.Method != "browser-chrome" {
		t.Fatalf("partial SSR thread did not escalate to the browser: method=%q rungs=%v error=%q",
			result.Method, result.Rungs, result.Error)
	}
	if got := strings.Join(result.Rungs, ","); got != "direct,browser" {
		t.Fatalf("rungs=%s, want direct,browser (the readers cannot load more comments)", got)
	}
	if !strings.Contains(result.Content, "u/delta_placeholder") ||
		!strings.Contains(result.Partial, "8 of 12 comments") {
		t.Fatalf("browser render not used: partial=%q", result.Partial)
	}
}

func TestRedditPartialPageIsKeptAndFlaggedWhenTheBrowserCannotDoBetter(t *testing.T) {
	for _, tc := range []struct {
		name    string
		browser *bool
		spy     *browserSpyConverter
		rungs   string
	}{
		{"browser off", browserOff(), &browserSpyConverter{}, "direct"},
		{"browser walled", browserOn(), &browserSpyConverter{
			html: "<html><body><h1>Prove your humanity</h1></body></html>", status: http.StatusOK,
		}, "direct,browser"},
		{"browser outage", browserOn(), &browserSpyConverter{err: errors.New("chrome missing")}, "direct,browser"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := pageHarvester(t, wallFixture(t, "reddit-thread-ssr.html"), tc.spy, tc.browser)
			result := h.Fetch(context.Background(), "https://www.reddit.com/r/examplesub/comments/abc123/")
			if result.Error != "" || result.Method != rungDirect {
				t.Fatalf(
					"partial thread not kept: method=%q rungs=%v error=%q",
					result.Method,
					result.Rungs,
					result.Error,
				)
			}
			if got := strings.Join(result.Rungs, ","); got != tc.rungs {
				t.Fatalf("rungs=%s, want %s", got, tc.rungs)
			}
			if !strings.Contains(result.Partial, "3 of 12 comments loaded") ||
				!strings.HasPrefix(result.Content, partialMarkerPrefix) {
				t.Fatalf("partial thread reported as complete: partial=%q", result.Partial)
			}
			for _, want := range []string{"u/alpha_placeholder", "  - **u/bravo_placeholder**", "u/foxtrot_placeholder"} {
				if !strings.Contains(result.Content, want) {
					t.Fatalf("templated SSR comment %q missing: %.600q", want, result.Content)
				}
			}
		})
	}
}

// TestRedditListingPageIsNotReadAsAThread: a subreddit listing carries one
// <shreddit-post> card per post inside <shreddit-feed>. The thread extractor
// must answer false for it so the listing takes the generic path — never the
// first card rendered as a comment-less "thread", flagged partial against
// that card's comment count.
func TestRedditListingPageIsNotReadAsAThread(t *testing.T) {
	listing := `<html><body><main><shreddit-feed>` +
		`<article><shreddit-post permalink="/r/examplesub/comments/aaa111/first_card/" comment-count="40"` +
		` post-title="First card title" subreddit-prefixed-name="r/examplesub"` +
		` author="card_one"></shreddit-post></article>` +
		`<article><shreddit-post permalink="/r/examplesub/comments/bbb222/second_card/" comment-count="7"` +
		` post-title="Second card title" subreddit-prefixed-name="r/examplesub"` +
		` author="card_two"></shreddit-post></article>` +
		`</shreddit-feed></main></body></html>`
	doc, err := html.Parse(strings.NewReader(listing))
	if err != nil {
		t.Fatal(err)
	}
	if extraction, extractor, ok := extractForSite("https://www.reddit.com/r/examplesub/", doc); ok {
		t.Fatalf("extractor %q read a subreddit listing as a thread (partial %q):\n%s",
			extractor, extraction.partial, extraction.markdown)
	}
}

// redditThreadPage is a minimal thread page: the post stating its comment
// count, one top-level comment per body and, when loader is set, one
// unexpanded "1 more reply" loader.
func redditThreadPage(stated int, bodies []string, loader bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<html><body><main><shreddit-post permalink="/r/examplesub/comments/ccc333/loader_thread/"`+
		` comment-count="%d" post-title="Loader thread" author="op_placeholder"></shreddit-post>`+
		`<shreddit-comment-tree>`, stated)
	for index, body := range bodies {
		fmt.Fprintf(&b, `<shreddit-comment thingid="t1_c%d" author="user_%d" score="2" depth="0">`+
			`<div slot="comment"><p>%s</p></div></shreddit-comment>`, index, index, body)
	}
	if loader {
		b.WriteString(`<faceplate-partial src="/svc/shreddit/more-comments/examplesub/t3_ccc333">` +
			`<button>1 more reply</button></faceplate-partial>`)
	}
	b.WriteString(`</shreddit-comment-tree></main></body></html>`)
	return b.String()
}

// TestRedditCompleteBrowserRenderBeatsTheFlaggedPage: the plain GET holds all
// but one short reply behind a "1 more reply" loader; the browser presses it
// and renders the whole thread. The complete render carries no partial
// marker and no gap list, so it is SHORTER than the flagged page — it must
// still win, never lose to the incomplete artifact on length.
func TestRedditCompleteBrowserRenderBeatsTheFlaggedPage(t *testing.T) {
	long := strings.Repeat("A substantive comment about the placeholder topic with real detail. ", 3)
	bodies := []string{long, long, long}
	spy := &browserSpyConverter{
		html:   redditThreadPage(4, append(append([]string(nil), bodies...), "Agreed."), false),
		status: http.StatusOK,
	}
	h := pageHarvester(t, redditThreadPage(4, bodies, true), spy, browserOn())
	result := h.Fetch(context.Background(), "https://www.reddit.com/r/examplesub/comments/ccc333/loader_thread/")
	if result.Error != "" || result.Method != "browser-chrome" || result.Partial != "" {
		t.Fatalf("the complete browser render lost to the flagged page: method=%q partial=%q error=%q",
			result.Method, result.Partial, result.Error)
	}
	if !strings.Contains(result.Content, "Agreed.") {
		t.Fatalf("the pressed reply is missing from the stored thread: %.600q", result.Content)
	}
}

// TestRedditSearchLinkFilterKeepsOtherDomainsLinks: only Reddit's own search
// links render as bare text; a host merely ending in "reddit.com" keeps its link.
func TestRedditSearchLinkFilterKeepsOtherDomainsLinks(t *testing.T) {
	page := redditThreadPage(1, []string{
		`See <a href="https://notreddit.com/search?q=tides">a search elsewhere</a> and ` +
			`<a href="https://www.reddit.com/search/?q=tides">tides</a>.`,
	}, false)
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatal(err)
	}
	extraction, _, ok := extractForSite("https://www.reddit.com/r/examplesub/comments/ccc333/loader_thread/", doc)
	if !ok {
		t.Fatal("the Reddit extractor did not claim a thread page")
	}
	if !strings.Contains(extraction.markdown, "[a search elsewhere](https://notreddit.com/search?q=tides)") {
		t.Fatalf("a link to another domain was flattened to text:\n%s", extraction.markdown)
	}
	if strings.Contains(extraction.markdown, "reddit.com/search/") {
		t.Fatalf("Reddit's own search link was kept:\n%s", extraction.markdown)
	}
}

// TestPartialPageEscalatesOnlyWhenARenderCanCloseAGap: a render presses
// loaders but never follows a "Continue this thread" link, so a thread whose
// only gap is a link Go could not follow (here one outside any comment, so
// no comment's page holds its replies) is stored at the HTTP rung, flagged,
// without spending the browser or the readers. A thread with a loader Go
// failed to follow, and a generic page the recall gate flags, still escalate.
func TestPartialPageEscalatesOnlyWhenARenderCanCloseAGap(t *testing.T) {
	long := strings.Repeat("A substantive comment about the placeholder topic with real detail. ", 3)
	bodies := []string{long, long, long}
	continueLink := `<a href="/r/examplesub/comments/ccc333/loader_thread/c0/">Continue this thread</a>` +
		`</shreddit-comment-tree>`
	withContinue := func(page string) string {
		return strings.Replace(page, `</shreddit-comment-tree>`, continueLink, 1)
	}
	walled := "<html><body><h1>Prove your humanity</h1></body></html>"
	thread := "https://www.reddit.com/r/examplesub/comments/ccc333/loader_thread/"
	for _, tc := range []struct {
		name, page, source, gap, rungs string
		convert                        Converter
		browserCalls                   int
	}{
		{
			"continue link only", withContinue(redditThreadPage(3, bodies, false)), thread,
			`"Continue this thread" link(s) not followed`, "direct", nil, 0,
		},
		{
			"continue link and comments not in the page", withContinue(redditThreadPage(5, bodies, false)), thread,
			"2 not in the page", "direct", nil, 0,
		},
		{
			"a loader among other gaps", withContinue(redditThreadPage(6, bodies, true)), thread,
			`unexpanded "more replies"`, "direct,browser", nil, 2,
		},
		{
			"generic partial page", catalogPage(), "https://guide.example.test/birds",
			"main-content extraction kept", "direct,browser", leadOnlyConverter(), 2,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spy := &browserSpyConverter{html: walled, status: http.StatusOK}
			if tc.convert != nil {
				spy.convertFn = tc.convert.Convert
			}
			h := pageHarvester(t, tc.page, spy, browserOn())
			result := h.Fetch(context.Background(), tc.source)
			if result.Error != "" || result.Method != rungDirect {
				t.Fatalf("partial page not kept: method=%q rungs=%v error=%q",
					result.Method, result.Rungs, result.Error)
			}
			if got := strings.Join(result.Rungs, ","); got != tc.rungs || spy.browserCalls != tc.browserCalls {
				t.Fatalf(
					"rungs=%s browser renders=%d, want %s and %d",
					got,
					spy.browserCalls,
					tc.rungs,
					tc.browserCalls,
				)
			}
			if !strings.Contains(result.Partial, tc.gap) || !strings.HasPrefix(result.Content, partialMarkerPrefix) {
				t.Fatalf("the gap is not flagged: partial=%q", result.Partial)
			}
		})
	}
}

// TestAShortCompleteRedditThreadIsStoredFromTheExtractor: a thread with a short
// post and one short comment renders under the 500-char thin-page floor. The
// floor catches JS shells and bot walls; a page the Reddit extractor claimed
// is neither, so its artifact is stored at the HTTP rung, never dropped.
func TestAShortCompleteRedditThreadIsStoredFromTheExtractor(t *testing.T) {
	page := redditThreadPage(1, []string{"Same here, thanks."}, false)
	for _, tc := range []struct {
		name    string
		browser *bool
	}{{"browser off", browserOff()}, {"browser on", browserOn()}} {
		t.Run(tc.name, func(t *testing.T) {
			spy := &browserSpyConverter{html: page, status: http.StatusOK}
			h := pageHarvester(t, page, spy, tc.browser)
			result := h.Fetch(
				context.Background(),
				"https://www.reddit.com/r/examplesub/comments/ccc333/loader_thread/",
			)
			if result.Error != "" || result.Method != rungDirect || result.Partial != "" {
				t.Fatalf("short complete thread not stored at direct: method=%q rungs=%v partial=%q error=%q",
					result.Method, result.Rungs, result.Partial, result.Error)
			}
			if !strings.Contains(result.Content, "# Loader thread") ||
				!strings.Contains(result.Content, "Same here, thanks.") {
				t.Fatalf("the extractor's artifact was not stored: %q", result.Content)
			}
		})
	}
}

// TestAShortCompleteRedditThreadRenderedByTheBrowserIsStored: the HTTP rungs
// meet a wall whose text is longer than the thread, and the browser renders
// a complete thread under the 500-char thin-page floor. The floor and the
// "longer than the earlier rungs" test catch JS shells and walls; a render
// the Reddit extractor claimed is neither, so it is stored, never dropped.
func TestAShortCompleteRedditThreadRenderedByTheBrowserIsStored(t *testing.T) {
	wallText := strings.Repeat("Your request has been blocked by network security. ", 12)
	spy := &browserSpyConverter{
		html:   redditThreadPage(1, []string{"Same here, thanks."}, false),
		status: http.StatusOK,
		convertFn: func(_ context.Context, _, _ string, _ []byte) (string, error) {
			return wallText, nil
		},
	}
	h := wallHarvester(t, spy, browserOn())
	result := h.Fetch(context.Background(), "https://www.reddit.com/r/examplesub/comments/ccc333/loader_thread/")
	if result.Error != "" || result.Method != "browser-chrome" || result.Partial != "" {
		t.Fatalf("short complete browser-rendered thread not stored: method=%q rungs=%v partial=%q error=%q",
			result.Method, result.Rungs, result.Partial, result.Error)
	}
	if !strings.Contains(result.Content, "# Loader thread") || !strings.Contains(result.Content, "Same here, thanks.") {
		t.Fatalf("the extractor's artifact was not stored: %q", result.Content)
	}
}

const loaderThread = "https://www.reddit.com/r/examplesub/comments/ddd444/loader_walk/"

// Reddit markup builders, shaped like the server-rendered thread and its
// loader answers: a comment's replies nest inside it, a loader carries its
// cursor as a hidden input, and every comment carries a "Continue this thread"
// link to its own page in a fold-more block. The block is hidden while the
// comment's replies are in the page; a comment at the page's depth limit (the
// fold) shows it instead of its replies.
func threadComment(id, author, body string, children ...string) string {
	return fmt.Sprintf(`<shreddit-comment thingid="t1_%s" author="%s" score="3" `+
		`permalink="/r/examplesub/comments/ddd444/comment/%s/">`+
		`<div slot="comment"><p>%s</p></div>`+
		`<div id="comment-children">%s</div>`+
		`<div class="fold-more hidden">%s</div></shreddit-comment>`,
		id, author, id, body, strings.Join(children, ""), foldLink(id))
}

// foldedComment is a comment at the fold: its replies are not in the page,
// its fold-more block is shown, and its link leads to them.
func foldedComment(id, author, body string) string {
	return fmt.Sprintf(`<shreddit-comment thingid="t1_%s" author="%s" score="3" `+
		`permalink="/r/examplesub/comments/ddd444/comment/%s/">`+
		`<div slot="comment"><p>%s</p></div>`+
		`<div id="comment-children" class="hidden"></div>`+
		`<div class="fold-more">%s</div></shreddit-comment>`,
		id, author, id, body, foldLink(id))
}

func foldLink(id string) string {
	return fmt.Sprintf(`<div data-more-replies-link><div class="more-comments-link" slot="more-comments-permalink">`+
		`<a href="/r/examplesub/comments/ddd444/comment/%s/?force-legacy-sct=1">Continue this thread</a></div></div>`, id)
}

func moreRepliesLoader(cursor string, replies int) string {
	return fmt.Sprintf(`<faceplate-partial loading="action" method="post" slot="children" `+
		`src="/svc/shreddit/more-comments/examplesub/t3_ddd444?sort=CONFIDENCE&amp;at=%s">`+
		`<input type="hidden" name="cursor" value="%s"><button>%d more replies</button></faceplate-partial>`,
		cursor, cursor, replies)
}

func viewMoreLoader(cursor string) string {
	return fmt.Sprintf(`<faceplate-partial loading="lazy" method="post" `+
		`src="/svc/shreddit/more-comments/examplesub/t3_ddd444?top-level=1&amp;at=%s">`+
		`<input type="hidden" name="cursor" value="%s"></faceplate-partial>`, cursor, cursor)
}

// deepThreadLink is how a continued page links a reply chain past its own
// depth: an "N more replies" link to the comment's page in the comment's
// children, the fold-more copy of it hidden.
func deepThreadLink(id string, replies int) string {
	return fmt.Sprintf(`<a class="more-comments-link" slot="children" `+
		`href="/r/examplesub/comments/ddd444/comment/%s/?force-legacy-sct=1">`+
		`<faceplate-number number="%d"></faceplate-number> more replies</a>`, id, replies)
}

// continueThreadLink is a shown fold-more block standing for id's replies.
func continueThreadLink(id string) string {
	return `<div class="fold-more">` + foldLink(id) + `</div>`
}

func loaderThreadPage(stated int, tree ...string) string {
	return fmt.Sprintf(`<html><head><title>Loader walk</title></head><body><main>`+
		`<shreddit-post permalink="/r/examplesub/comments/ddd444/loader_walk/" comment-count="%d" `+
		`post-title="Loader walk" author="op_placeholder" subreddit-prefixed-name="r/examplesub">`+
		`<div slot="text-body"><p>Opening post.</p></div></shreddit-post>`+
		`<shreddit-comment-tree>%s</shreddit-comment-tree></main></body></html>`, stated, strings.Join(tree, ""))
}

// redditSite answers a thread page, its loaders (by cursor) and its
// "Continue this thread" pages (by comment id), recording every request.
type redditSite struct {
	mu        sync.Mutex
	page      string
	fragments map[string]string // cursor -> fragment
	threads   map[string]string // comment id -> continued page
	status    map[string]int    // cursor or comment id -> an error status to answer instead
	walls     map[string]string // cursor -> a page to answer instead
	requests  []redditRequest
}

type redditRequest struct {
	method, path, referer, accept, origin, cookie, cursor string
}

func (site *redditSite) roundTrip(request *http.Request) (*http.Response, error) {
	cursor := ""
	if request.Method == http.MethodPost {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		form, err := url.ParseQuery(string(body))
		if err != nil {
			return nil, err
		}
		cursor = form.Get("cursor")
	}
	site.mu.Lock()
	site.requests = append(site.requests, redditRequest{
		method:  request.Method,
		path:    request.URL.Path,
		referer: request.Header.Get("Referer"),
		accept:  request.Header.Get("Accept"),
		origin:  request.Header.Get("Origin"),
		cookie:  request.Header.Get("Cookie"),
		cursor:  cursor,
	})
	site.mu.Unlock()
	path := request.URL.Path
	switch {
	case strings.HasPrefix(path, "/svc/shreddit/more-comments/"):
		if status := site.status[cursor]; status != 0 {
			return response(request, status, "text/html", "<html><body>refused</body></html>"), nil
		}
		if wall, ok := site.walls[cursor]; ok {
			return response(request, http.StatusOK, "text/html; charset=utf-8", wall), nil
		}
		if fragment, ok := site.fragments[cursor]; ok {
			answer := response(request, http.StatusOK, "text/vnd.reddit.partial+html; charset=utf-8", fragment)
			answer.Header.Set("Set-Cookie", "loid=placeholder-session; Path=/; Domain=.reddit.com; Secure")
			return answer, nil
		}
	case strings.Contains(path, "/comment/"):
		id := strings.TrimSuffix(path[strings.LastIndex(strings.TrimSuffix(path, "/"), "/")+1:], "/")
		if status := site.status[id]; status != 0 {
			return response(request, status, "text/html", "<html><body>refused</body></html>"), nil
		}
		if page, ok := site.threads[id]; ok {
			return response(request, http.StatusOK, "text/html; charset=utf-8", page), nil
		}
	case path == "/r/examplesub/comments/ddd444/loader_walk/":
		return response(request, http.StatusOK, "text/html; charset=utf-8", site.page), nil
	}
	return response(request, http.StatusNotFound, "text/html", "<html><body>not found</body></html>"), nil
}

func (site *redditSite) harvester(
	t *testing.T,
	spy *browserSpyConverter,
	browserRung *bool,
) (*Harvester, *pacingClock) {
	t.Helper()
	missing := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusNotFound, "application/json", `{}`), nil
	})
	pacing := newPacingClock()
	return mustNew(t, Options{
		CacheDir:    t.TempDir(),
		Client:      &http.Client{Transport: roundTripFunc(site.roundTrip)},
		Chrome:      &http.Client{Transport: roundTripFunc(site.roundTrip)},
		Jina:        &http.Client{Transport: missing},
		OA:          &http.Client{Transport: missing},
		Converter:   spy,
		BrowserRung: browserRung,
		Clock:       pacing,
	}), pacing
}

// walkedThread is a thread whose 14 stated comments are served as: two in
// the page with a "2 more replies" loader, a chain past the page's depth
// behind "Continue this thread" (whose page holds a loader of its own), two
// "View more comments" pages, one deleted and one removed placeholder, and
// one comment Reddit does not serve at all.
func walkedThread() *redditSite {
	echo := func(children ...string) string {
		return threadComment("c5", "echo_placeholder", "Echo, deep in the chain.", children...)
	}
	page := loaderThreadPage(14,
		threadComment("c1", "alpha_placeholder", "Alpha top comment.",
			threadComment("c2", "bravo_placeholder", "Bravo reply."),
			moreRepliesLoader("cur-a", 2)),
		threadComment("c3", "charlie_placeholder", "Charlie top comment.",
			threadComment("c4", "delta_placeholder", "Delta reply.", echo(continueThreadLink("c5")))),
		viewMoreLoader("cur-top"),
	)
	foxtrot := threadComment("c6", "foxtrot_placeholder", "Just a moment: checking your browser settings fixed it.")
	deleted := threadComment("c7", "[deleted]", "[deleted]",
		threadComment("c8", "golf_placeholder", "Golf answering a deleted comment."))
	hotel := threadComment("c9", "hotel_placeholder", "Hotel, past the depth limit.", moreRepliesLoader("cur-b", 1))
	juliet := threadComment("c11", "juliet_placeholder", "Juliet, second page of top comments.")
	kilo := threadComment("c12", "kilo_placeholder", "Kilo, last page.")
	lima := threadComment("c13", "lima_placeholder", "[removed]")
	return &redditSite{
		page: page,
		fragments: map[string]string{
			"cur-a":    foxtrot + deleted,
			"cur-b":    threadComment("c10", "india_placeholder", "India, loaded on the continued page."),
			"cur-top":  juliet + viewMoreLoader("cur-top2"),
			"cur-top2": kilo + lima,
		},
		threads: map[string]string{"c5": loaderThreadPage(14, echo(hotel))},
	}
}

// commentHeaders are the comment header lines of a rendered thread, in order.
func commentHeaders(markdown string) []string {
	var headers []string
	for _, line := range strings.Split(markdown, "\n") {
		if strings.HasPrefix(strings.TrimLeft(line, " "), "- **") {
			if cut := strings.Index(line, " · "); cut >= 0 {
				line = line[:cut]
			}
			headers = append(headers, line)
		}
	}
	return headers
}

// TestRedditLoadersAreFollowedIntoTheTree: every "more replies" and "View
// more comments" loader is requested from Go (its cursor POSTed with the
// thread as Referer), every "Continue this thread" page is fetched, loaders
// inside what they load are followed too, and each answer lands where its
// loader stood — so the thread renders whole, in thread order and nesting,
// with the one comment Reddit does not serve named rather than flagged.
func TestRedditLoadersAreFollowedIntoTheTree(t *testing.T) {
	site := walkedThread()
	h, pacing := site.harvester(t, &browserSpyConverter{}, browserOff())
	result := h.FetchWithOptions(context.Background(), loaderThread, FetchOptions{Refresh: true})
	if result.Error != "" || result.Partial != "" || result.Method != rungDirect {
		t.Fatalf("the followed thread is not complete at the direct rung: method=%q partial=%q error=%q",
			result.Method, result.Partial, result.Error)
	}
	want := []string{
		"- **u/alpha_placeholder**",
		"  - **u/bravo_placeholder**",
		"  - **u/foxtrot_placeholder**",
		"  - **[deleted]**",
		"    - **u/golf_placeholder**",
		"- **u/charlie_placeholder**",
		"  - **u/delta_placeholder**",
		"    - **u/echo_placeholder**",
		"      - **u/hotel_placeholder**",
		"        - **u/india_placeholder**",
		"- **u/juliet_placeholder**",
		"- **u/kilo_placeholder**",
		"- **u/lima_placeholder**",
	}
	if got := commentHeaders(result.Content); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("thread order or nesting is wrong:\n got %q\nwant %q\n%s", got, want, result.Content)
	}
	countLine := "**Comments:** 14 stated · 13 loaded (1 deleted, 1 removed) · 1 not served by Reddit"
	if !strings.Contains(result.Content, countLine) || strings.Contains(result.Content, "gaps:") {
		t.Fatalf("the count does not reconcile to the stated 14: %.700q", result.Content)
	}
	wantRequests := []string{
		"GET /r/examplesub/comments/ddd444/loader_walk/",
		"POST /svc/shreddit/more-comments/examplesub/t3_ddd444 cur-a",
		"GET /r/examplesub/comments/ddd444/comment/c5/",
		"POST /svc/shreddit/more-comments/examplesub/t3_ddd444 cur-top",
		"POST /svc/shreddit/more-comments/examplesub/t3_ddd444 cur-b",
		"POST /svc/shreddit/more-comments/examplesub/t3_ddd444 cur-top2",
	}
	var got []string
	for index, request := range site.requests {
		got = append(got, strings.TrimSpace(request.method+" "+request.path+" "+request.cursor))
		if index == 0 {
			continue
		}
		if request.referer != loaderThread {
			t.Fatalf("loader request %d carried Referer %q, want the thread %q", index, request.referer, loaderThread)
		}
		if request.method == http.MethodPost && (!strings.HasPrefix(request.accept, "text/vnd.reddit.partial+html") ||
			request.origin != "https://www.reddit.com") {
			t.Fatalf("loader request %d asked for %q from origin %q, not the partial from Reddit",
				index, request.accept, request.origin)
		}
		if index >= 2 && !strings.Contains(request.cookie, "loid=placeholder-session") {
			t.Fatalf("loader request %d left the cookie session the first answer set: Cookie %q", index, request.cookie)
		}
	}
	if strings.Join(got, "\n") != strings.Join(wantRequests, "\n") {
		t.Fatalf("requests:\n%s\nwant (a folded comment's own permalink is never fetched):\n%s",
			strings.Join(got, "\n"), strings.Join(wantRequests, "\n"))
	}
	if len(pacing.sleeps) != len(wantRequests)-1 {
		t.Fatalf("paced %d times for %d loader requests: %v", len(pacing.sleeps), len(wantRequests)-1, pacing.sleeps)
	}
	for _, pause := range pacing.sleeps {
		if pause != loaderPace {
			t.Fatalf("a loader request was paced %v, want %v", pause, loaderPace)
		}
	}
	again := h.FetchWithOptions(context.Background(), loaderThread, FetchOptions{Refresh: true})
	if again.Content != result.Content {
		t.Fatalf("a second harvest of the same thread differs:\n%s\n---\n%s", result.Content, again.Content)
	}
}

// TestRedditFoldedRepliesInLoadedBranchesAreFollowed: a loader's answer and a
// "Continue this thread" page each render only a few levels of the tree. A
// comment at a loader answer's fold shows its fold-more block — the same slot
// every comment hides a copy of its permalink in — instead of its replies; a
// continued page links a chain past its depth as "N more replies" to the
// comment's page. Both are followed, page after page, until the deepest live
// reply is in the tree; the hidden copies are never fetched.
func TestRedditFoldedRepliesInLoadedBranchesAreFollowed(t *testing.T) {
	charlie := "Charlie, at the loader answer's fold."
	delta := "Delta, past the continued page's depth."
	site := &redditSite{
		page: loaderThreadPage(5, threadComment("c1", "alpha_placeholder", "Alpha top comment.",
			moreRepliesLoader("cur-deep", 4))),
		fragments: map[string]string{
			"cur-deep": threadComment("c2", "bravo_placeholder", "Bravo, in the loaded branch.",
				foldedComment("c3", "charlie_placeholder", charlie)),
		},
		threads: map[string]string{
			"c3": loaderThreadPage(5, threadComment("c3", "charlie_placeholder", charlie,
				threadComment("c4", "delta_placeholder", delta, deepThreadLink("c4", 1)))),
			"c4": loaderThreadPage(5, threadComment("c4", "delta_placeholder", delta,
				threadComment("c5", "echo_placeholder", "Echo, the deepest live reply."))),
		},
	}
	h, _ := site.harvester(t, &browserSpyConverter{}, browserOff())
	result := h.FetchWithOptions(context.Background(), loaderThread, FetchOptions{Refresh: true})
	want := []string{
		"- **u/alpha_placeholder**",
		"  - **u/bravo_placeholder**",
		"    - **u/charlie_placeholder**",
		"      - **u/delta_placeholder**",
		"        - **u/echo_placeholder**",
	}
	if got := commentHeaders(result.Content); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the folded chain did not reach its deepest reply:\n got %q\nwant %q\n%s", got, want, result.Content)
	}
	if result.Error != "" || result.Partial != "" ||
		!strings.Contains(result.Content, "**Comments:** 5 stated · 5 loaded (0 deleted, 0 removed)\n") {
		t.Fatalf("the thread does not reconcile to its stated 5: partial=%q error=%q %.600q",
			result.Partial, result.Error, result.Content)
	}
	wantRequests := []string{
		"GET /r/examplesub/comments/ddd444/loader_walk/",
		"POST /svc/shreddit/more-comments/examplesub/t3_ddd444 cur-deep",
		"GET /r/examplesub/comments/ddd444/comment/c3/",
		"GET /r/examplesub/comments/ddd444/comment/c4/",
	}
	var got []string
	for _, request := range site.requests {
		got = append(got, strings.TrimSpace(request.method+" "+request.path+" "+request.cursor))
	}
	if strings.Join(got, "\n") != strings.Join(wantRequests, "\n") {
		t.Fatalf("requests:\n%s\nwant (a hidden fold-more copy is never fetched):\n%s",
			strings.Join(got, "\n"), strings.Join(wantRequests, "\n"))
	}
}

// TestRedditRemainderWithNoLoaderLeftIsNamedNotFlagged: a thread whose page
// holds no loader and no link states more comments than it serves. Nothing
// could load them, so the remainder is Reddit's unserved comments, named in
// the count line — the artifact is not flagged partial for them.
func TestRedditRemainderWithNoLoaderLeftIsNamedNotFlagged(t *testing.T) {
	long := strings.Repeat("A substantive comment about the placeholder topic with real detail. ", 3)
	spy := &browserSpyConverter{}
	h := pageHarvester(t, redditThreadPage(5, []string{long, long, long}, false), spy, browserOn())
	result := h.Fetch(context.Background(), "https://www.reddit.com/r/examplesub/comments/ccc333/loader_thread/")
	if result.Error != "" || result.Method != rungDirect || result.Partial != "" || spy.browserCalls != 0 {
		t.Fatalf("a thread with nothing left to load was flagged or escalated: method=%q rungs=%v partial=%q",
			result.Method, result.Rungs, result.Partial)
	}
	if !strings.Contains(result.Content, "**Comments:** 5 stated · 3 loaded (0 deleted, 0 removed) · "+
		"2 not served by Reddit (removed by its filters, or deleted without a placeholder)") {
		t.Fatalf("the unserved remainder is not named: %.600q", result.Content)
	}
}

// TestRedditContinuedThreadServedEmptyIsNotAGap: a "Continue this thread"
// link leads to the comment's own page, and Reddit serves that page with the
// comment's tree empty (it holds none of the replies the count includes).
// The link was followed and answered, so it is no gap: the replies are part
// of what Reddit does not serve. A page that is not that comment's tree at
// all is still a failed link, flagged.
func TestRedditContinuedThreadServedEmptyIsNotAGap(t *testing.T) {
	empty := `<html><head><title>Reddit - The heart of the internet</title></head><body>` +
		`<shreddit-post permalink="/r/examplesub/comments/ddd444/loader_walk/" comment-count="4"></shreddit-post>` +
		`<shreddit-comment-tree-stats total-comments="0"></shreddit-comment-tree-stats>` +
		`<shreddit-comment-tree thingid="t1_c2"></shreddit-comment-tree></body></html>`
	for _, tc := range []struct {
		name, continued, partial string
	}{
		{"served empty", empty, ""},
		{
			"another page", `<html><head><title>Elsewhere</title></head><body><p>Not a thread.</p></body></html>`,
			`"Continue this thread" link(s) not followed`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			site := &redditSite{
				page: loaderThreadPage(4, threadComment("c1", "alpha_placeholder", "Alpha.",
					threadComment("c2", "bravo_placeholder", "Bravo, deep.", continueThreadLink("c2")))),
				threads: map[string]string{"c2": tc.continued},
			}
			h, _ := site.harvester(t, &browserSpyConverter{}, browserOff())
			result := h.Fetch(context.Background(), loaderThread)
			if result.Error != "" || result.Method != rungDirect || len(site.requests) != 2 {
				t.Fatalf("thread not kept after one link request: method=%q requests=%+v error=%q",
					result.Method, site.requests, result.Error)
			}
			if tc.partial == "" {
				if result.Partial != "" || !strings.Contains(result.Content, "2 loaded (0 deleted, 0 removed) · "+
					"2 not served by Reddit") {
					t.Fatalf("an empty continuation was flagged or not reconciled: partial=%q %.500q",
						result.Partial, result.Content)
				}
				return
			}
			if !strings.Contains(result.Partial, tc.partial) ||
				!strings.Contains(result.Partial, `titled "Elsewhere"`) {
				t.Fatalf("a failed continuation is not flagged: %q", result.Partial)
			}
		})
	}
}
