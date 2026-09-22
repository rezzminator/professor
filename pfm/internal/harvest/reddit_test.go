package harvest

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
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
		"**Comments:** 12 stated · 8 in this page (1 deleted, 1 removed)",
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
	if !strings.Contains(extraction.partial, "8 of 12 comments in the page") {
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
			if !strings.Contains(result.Partial, "3 of 12 comments in the page") ||
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
// loaders but never follows a "Continue this thread" link and cannot conjure
// comments missing from the page, so a thread whose only gaps are those is
// stored at the HTTP rung, flagged, without spending the browser or the
// readers. A thread with a loader, and a generic page the recall gate flags,
// still escalate.
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
			"comments not in the page only", redditThreadPage(5, bodies, false), thread,
			"2 not in the page", "direct", nil, 0,
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
