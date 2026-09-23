package harvest

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"golang.org/x/net/html"
)

// A Discourse forum on a domain that is not meta.discourse.org: the extractor
// knows it by its generator meta alone.
const (
	discourseHost     = "forum.example.test"
	discourseTopicURL = "https://" + discourseHost + "/t/placeholder-topic/4242"
)

// discoursePost is one crawler post of a fixture topic.
type discoursePost struct {
	number int
	author string
	body   string
	likes  int
}

// discourseFixturePosts is a topic of seven posts over three crawler pages,
// numbered as Discourse numbers them: post 5 (a small action the crawler view
// leaves out) is missing from the numbering.
func discourseFixturePosts() [][]discoursePost {
	return [][]discoursePost{
		{
			{1, "opener_placeholder", `<p>Opening post <img src="https://emoji.example.test/wave.png" ` +
				`title=":wave:" class="emoji" alt=":wave:"> of the topic.</p>`, 12},
			{2, "alpha_placeholder", `<aside class="quote"><div class="title"><img class="avatar" ` +
				`src="/avatar.png"> opener_placeholder:</div><blockquote><p>Opening post</p></blockquote></aside>` +
				`<p>A reply quoting the opener.</p>`, 1},
			{3, "bravo_placeholder", `<p>Third post, with an image:</p><p><div class="lightbox-wrapper">` +
				`<a class="lightbox" href="/uploads/big.png"><img src="/uploads/small.png" alt="diagram">` +
				`<div class="meta"><span class="filename">diagram</span><span class="informations">800×600 12 KB` +
				`</span></div></a></div></p>`, 0},
		},
		{
			{4, "charlie_placeholder", "<p>Fourth post, first on page two.</p>", 0},
			{6, "delta_placeholder", "<p>Sixth post; five was a small action.</p>", 2},
		},
		{
			{7, "echo_placeholder", "<p>Seventh post.</p>", 0},
			{8, "foxtrot_placeholder", "<p>Eighth and last post.</p>", 0},
		},
	}
}

// discoursePageURL is the path (and query) of a topic page: page 1 is the
// topic's own address.
func discoursePageURL(page int) string {
	if page <= 1 {
		return "/t/placeholder-topic/4242"
	}
	return "/t/placeholder-topic/4242?page=" + strconv.Itoa(page)
}

// discourseCrawlerPage renders one crawler-view page as Discourse serves it to
// a client without JavaScript. generator false drops the generator meta.
func discourseCrawlerPage(pages [][]discoursePost, page int, generator bool) string {
	return discourseCrawlerPageAs(pages, page, generator, false)
}

// discourseCrawlerPageAs is discourseCrawlerPage; qa renders the topic as a
// forum running the solved plugin does: a schema.org QAPage whose Question
// holds the posts, in place of a DiscussionForumPosting.
func discourseCrawlerPageAs(pages [][]discoursePost, page int, generator, qa bool) string {
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html lang="en"><head><meta charset="utf-8">` +
		`<title>Placeholder topic - Support - Example Forum</title>`)
	if generator {
		b.WriteString(`<meta name="generator" content="Discourse 2026.10.0-latest - ` +
			`https://github.com/discourse/discourse version 0000000000000000000000000000000000000000">`)
	}
	fmt.Fprintf(&b, `<link rel="canonical" href="https://%s%s" />`, discourseHost, discoursePageURL(page))
	if page > 1 {
		fmt.Fprintf(&b, `<link rel="prev" href="%s">`, discoursePageURL(page-1))
	}
	if page < len(pages) {
		fmt.Fprintf(&b, `<link rel="next" href="%s">`, discoursePageURL(page+1))
	}
	b.WriteString(`</head><body class="crawler"><header><a href="/">Example Forum</a></header>` +
		`<div id="main-outlet" class="wrap" role="main"><div id="topic-title"><h1>` +
		`<a href="/t/placeholder-topic/4242">Placeholder topic</a></h1></div>`)
	if qa {
		b.WriteString(`<div itemscope="itemscope" itemtype="https://schema.org/QAPage">` +
			`<meta itemprop='name' content='Placeholder topic'>` +
			`<link itemprop='url' href='https://` + discourseHost + `/t/placeholder-topic/4242'>` +
			`<div itemprop="mainEntity" itemscope="itemscope" itemtype="https://schema.org/Question">` +
			`<meta itemprop='answerCount' content='6'>`)
	} else {
		b.WriteString(`<div itemscope="itemscope" itemtype="http://schema.org/DiscussionForumPosting">` +
			`<meta itemprop='headline' content='Placeholder topic'>` +
			`<link itemprop='url' href='https://` + discourseHost + `/t/placeholder-topic/4242'>` +
			`<meta itemprop='articleSection' content='Support'>`)
	}
	for _, post := range pages[page-1] {
		fmt.Fprintf(&b, `<div id='post_%d' itemprop="comment" itemscope="itemscope" `+
			`itemtype="http://schema.org/Comment" class='topic-body crawler-post'>`+
			`<div class='crawler-post-meta'><span class="creator" itemprop="author" itemscope="itemscope" `+
			`itemtype="http://schema.org/Person"><a rel='nofollow' href='https://%s/u/%s'>`+
			`<span itemprop="name">%s</span></a></span><span class="crawler-post-infos">`+
			`<time itemprop="datePublished" datetime='2026-03-%02dT10:%02d:00Z' class='post-time'>March %d, 2026`+
			`</time><span itemprop="position">%d</span></span></div>`+
			`<div class='post' itemprop="text">%s</div>`+
			`<div itemprop="interactionStatistic" itemscope="itemscope" itemtype="http://schema.org/InteractionCounter">`+
			`<meta itemprop="interactionType" content="http://schema.org/LikeAction"/>`+
			`<meta itemprop="userInteractionCount" content="%d" /><span class='post-likes'>%d Likes</span></div>`+
			`</div>`,
			post.number, discourseHost, post.author, post.author, post.number, post.number, post.number,
			post.number, post.body, post.likes, post.likes)
	}
	b.WriteString(`</div>`)
	if qa {
		b.WriteString(`</div>`)
	}
	if page < len(pages) {
		fmt.Fprintf(&b, `<div role='navigation' itemscope itemtype='http://schema.org/SiteNavigationElement' `+
			`class="topic-body crawler-post"><span itemprop='name'><b><a rel="next" itemprop="url" href="%s">`+
			`next page →</a></b></span></div>`, discoursePageURL(page+1))
	}
	b.WriteString(`<div id="related-topics" role="complementary"><h3>Related topics</h3><table><tr>` +
		`<td><a href="/t/another-topic/99">Another topic</a></td><td>7</td></tr></table></div>` +
		`</div><footer class="container"><nav class='crawler-nav'><a href='/'>Home</a></nav></footer>` +
		`</body></html>`)
	return b.String()
}

// discourseSite serves a fixture topic's crawler pages, its JSON, and any
// scripted refusal, recording every request.
type discourseSite struct {
	mu        sync.Mutex
	pages     [][]discoursePost
	stated    int
	generator bool
	qa        bool
	status    map[string]int    // request URI -> an error status to answer instead
	answers   map[string]string // request URI -> a page to answer instead
	// redirects: an absolute request URL -> the Location a 302 answers with.
	redirects map[string]string
	// countJSON, when set, is the topic's JSON as served.
	countJSON string
	// anonymous serves every page without the topic's address (no canonical
	// link, no itemprop url).
	anonymous bool
	requests  []string
}

// discourseTopicAddressRe matches a crawler page's two statements of its
// topic's address.
var discourseTopicAddressRe = regexp.MustCompile(`<link (?:rel="canonical"|itemprop='url') [^>]*>`)

func newDiscourseSite(stated int) *discourseSite {
	return &discourseSite{pages: discourseFixturePosts(), stated: stated, generator: true}
}

func (site *discourseSite) roundTrip(request *http.Request) (*http.Response, error) {
	uri := request.URL.RequestURI()
	if strings.HasPrefix(request.URL.Path, "/t/") {
		// The topic's own requests; the images the artifact localizes are not.
		site.mu.Lock()
		site.requests = append(site.requests, request.Method+" "+uri)
		site.mu.Unlock()
	}
	if location := site.redirects[request.URL.String()]; location != "" {
		moved := response(request, http.StatusFound, "text/html", "")
		moved.Header.Set("Location", location)
		return moved, nil
	}
	if status := site.status[uri]; status != 0 {
		return response(request, status, "text/html", "<html><body>refused</body></html>"), nil
	}
	if answer, ok := site.answers[uri]; ok {
		return response(request, http.StatusOK, "text/html; charset=utf-8", answer), nil
	}
	if request.URL.Path == "/t/placeholder-topic/4242.json" {
		body := fmt.Sprintf(`{"id":4242,"posts_count":%d,"post_stream":{"posts":[]}}`, site.stated)
		if site.countJSON != "" {
			body = site.countJSON
		}
		return response(request, http.StatusOK, "application/json; charset=utf-8", body), nil
	}
	if site.anonymous {
		answer, err := site.page(request)
		if err == nil && answer.StatusCode == http.StatusOK {
			raw, readErr := io.ReadAll(answer.Body)
			if readErr != nil {
				return nil, readErr
			}
			return response(request, http.StatusOK, "text/html; charset=utf-8",
				discourseTopicAddressRe.ReplaceAllString(string(raw), "")), nil
		}
		return answer, err
	}
	return site.page(request)
}

// page answers a request for one of the topic's pages.
func (site *discourseSite) page(request *http.Request) (*http.Response, error) {
	if request.URL.Path == "/t/placeholder-topic/4242" || request.URL.Path == "/t/placeholder-topic/4242/6" {
		page := 1
		if raw := request.URL.Query().Get("page"); raw != "" {
			page, _ = strconv.Atoi(raw)
		}
		if request.URL.Path == "/t/placeholder-topic/4242/6" {
			// A post's own address: its crawler view holds that post alone.
			single := [][]discoursePost{{site.pages[1][1]}}
			return response(request, http.StatusOK, "text/html; charset=utf-8",
				discourseCrawlerPage(single, 1, site.generator)), nil
		}
		if page >= 1 && page <= len(site.pages) {
			return response(request, http.StatusOK, "text/html; charset=utf-8",
				discourseCrawlerPageAs(site.pages, page, site.generator, site.qa)), nil
		}
	}
	return response(request, http.StatusNotFound, "text/html", "<html><body>not found</body></html>"), nil
}

func (site *discourseSite) harvester(t *testing.T, converter Converter, browserRung *bool) (*Harvester, *pacingClock) {
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
		Converter:   converter,
		BrowserRung: browserRung,
		Clock:       pacing,
	}), pacing
}

// discoursePostHeaders returns the "## #N · author" part of every post
// heading in content, in order.
func discoursePostHeaders(content string) []string {
	var headers []string
	for _, line := range strings.Split(content, "\n") {
		if !strings.HasPrefix(line, "## #") {
			continue
		}
		parts := strings.SplitN(line, " · ", 3)
		headers = append(headers, strings.Join(parts[:min(2, len(parts))], " · "))
	}
	return headers
}

var discourseAllPosts = []string{
	"## #1 · opener_placeholder",
	"## #2 · alpha_placeholder",
	"## #3 · bravo_placeholder",
	"## #4 · charlie_placeholder",
	"## #6 · delta_placeholder",
	"## #7 · echo_placeholder",
	"## #8 · foxtrot_placeholder",
}

// TestDiscourseTopicIsFollowedAcrossItsPagesAndReconciles: a topic over three
// crawler pages. Its JSON is read for the stated count, every next page is
// followed in order at the loader pace, each post renders once in number
// order with its author, date and likes, and the count reconciles — the
// artifact is complete, not partial. A second harvest is identical.
func TestDiscourseTopicIsFollowedAcrossItsPagesAndReconciles(t *testing.T) {
	site := newDiscourseSite(7)
	h, pacing := site.harvester(t, &browserSpyConverter{}, browserOn())
	result := h.FetchWithOptions(context.Background(), discourseTopicURL, FetchOptions{Refresh: true})
	if result.Error != "" || result.Partial != "" || result.Method != rungDirect {
		t.Fatalf("the followed topic is not complete at the direct rung: method=%q rungs=%v partial=%q error=%q",
			result.Method, result.Rungs, result.Partial, result.Error)
	}
	if got := discoursePostHeaders(result.Content); strings.Join(got, "\n") != strings.Join(discourseAllPosts, "\n") {
		t.Fatalf("posts or their order are wrong:\n got %q\nwant %q\n%s", got, discourseAllPosts, result.Content)
	}
	for _, want := range []string{
		"# Placeholder topic",
		"**Category:** Support",
		"**Topic:** " + discourseTopicURL,
		"**Posts:** 7 stated · 7 loaded\n",
		"## #1 · opener_placeholder · 2026-03-01 10:01 UTC · 12 likes",
		"## #2 · alpha_placeholder · 2026-03-02 10:02 UTC · 1 like",
		"Opening post :wave: of the topic.",
		"> Opening post",
		"![diagram](https://" + discourseHost + "/uploads/small.png)",
	} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("the artifact lacks %q:\n%s", want, result.Content)
		}
	}
	for _, unwanted := range []string{"800×600", "Related topics", "Another topic", "next page →", "avatar.png"} {
		if strings.Contains(result.Content, unwanted) {
			t.Fatalf("the artifact carries page furniture %q:\n%s", unwanted, result.Content)
		}
	}
	wantRequests := []string{
		"GET /t/placeholder-topic/4242",
		"GET /t/placeholder-topic/4242.json",
		"GET /t/placeholder-topic/4242?page=2",
		"GET /t/placeholder-topic/4242?page=3",
	}
	if strings.Join(site.requests, "\n") != strings.Join(wantRequests, "\n") {
		t.Fatalf("requests:\n%s\nwant:\n%s", strings.Join(site.requests, "\n"), strings.Join(wantRequests, "\n"))
	}
	if len(pacing.sleeps) != len(wantRequests)-1 {
		t.Fatalf("paced %d times for %d loader requests", len(pacing.sleeps), len(wantRequests)-1)
	}
	again := h.FetchWithOptions(context.Background(), discourseTopicURL, FetchOptions{Refresh: true})
	if again.Content != result.Content {
		t.Fatalf("a second harvest of the same topic differs:\n%s\n---\n%s", result.Content, again.Content)
	}
}

// TestDiscourseIsRecognisedByItsGeneratorOnAnyDomain: the same crawler page on
// a domain no extractor names is claimed by the Discourse extractor through
// its generator meta — the injected converter never sees it — whether the
// topic is a DiscussionForumPosting or, on a forum running the solved plugin,
// a QAPage; without that meta it is a page like any other: the generic path
// converts it.
func TestDiscourseIsRecognisedByItsGeneratorOnAnyDomain(t *testing.T) {
	for _, tc := range []struct {
		name          string
		generator, qa bool
		conversion    int
	}{
		{"with the generator meta", true, false, 0},
		{"a solved-plugin QAPage", true, true, 0},
		{"without the generator meta", false, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			site := newDiscourseSite(7)
			site.generator, site.qa = tc.generator, tc.qa
			converter := &fakeConverter{}
			h, _ := site.harvester(t, converter, browserOff())
			result := h.FetchWithOptions(context.Background(), discourseTopicURL, FetchOptions{Refresh: true})
			if result.Error != "" {
				t.Fatalf("fetch failed: %q", result.Error)
			}
			if converter.calls != tc.conversion {
				t.Fatalf("the injected converter ran %d times, want %d", converter.calls, tc.conversion)
			}
			claimed := strings.Contains(result.Content, "**Posts:** 7 stated · 7 loaded")
			if claimed != tc.generator {
				t.Fatalf(
					"claimed by the Discourse extractor = %v, want %v:\n%.600s",
					claimed,
					tc.generator,
					result.Content,
				)
			}
		})
	}
}

// TestDiscourseStatedPostsInNoPageAreNamed: the topic states two posts more
// than any page serves, and no page link is left. The remainder is named on
// the count line and flags the artifact partial.
func TestDiscourseStatedPostsInNoPageAreNamed(t *testing.T) {
	site := newDiscourseSite(9)
	h, _ := site.harvester(t, &browserSpyConverter{}, browserOn())
	result := h.FetchWithOptions(context.Background(), discourseTopicURL, FetchOptions{Refresh: true})
	if result.Error != "" || result.Method != rungDirect || len(discoursePostHeaders(result.Content)) != 7 {
		t.Fatalf("topic not kept whole at the direct rung: method=%q rungs=%v error=%q", result.Method, result.Rungs,
			result.Error)
	}
	for _, want := range []string{"7 of 9 posts loaded", "2 stated post(s) in no page served"} {
		if !strings.Contains(result.Partial, want) {
			t.Fatalf("the partial marker lacks %q: %q", want, result.Partial)
		}
	}
	if !strings.Contains(result.Content, "**Posts:** 9 stated · 7 loaded · gaps: 2 stated post(s)") {
		t.Fatalf("the count line does not name the remainder:\n%.900s", result.Content)
	}
}

// TestDiscourseFollowingStopsAreNamedPartials: a rate limit, a page answered
// by something that is not the topic, the request cap and an unread count
// each leave the artifact partial with the posts not loaded and why.
func TestDiscourseFollowingStopsAreNamedPartials(t *testing.T) {
	t.Run("rate limited", func(t *testing.T) {
		site := newDiscourseSite(7)
		site.status = map[string]int{discoursePageURL(2): http.StatusTooManyRequests}
		h, _ := site.harvester(t, &browserSpyConverter{}, browserOn())
		result := h.FetchWithOptions(context.Background(), discourseTopicURL, FetchOptions{Refresh: true})
		if result.Error != "" || result.Method != rungDirect || len(site.requests) != 3 {
			t.Fatalf("a rate-limited topic: method=%q rungs=%v requests=%v error=%q",
				result.Method, result.Rungs, site.requests, result.Error)
		}
		for _, want := range []string{
			"3 of 7 posts loaded",
			"the posts after #3 are not loaded (next page, page 2)",
			"HTTP 429",
			"not retried",
		} {
			if !strings.Contains(result.Partial, want) {
				t.Fatalf("the partial marker lacks %q: %q", want, result.Partial)
			}
		}
	})
	t.Run("answered by a login page", func(t *testing.T) {
		site := newDiscourseSite(7)
		site.answers = map[string]string{discoursePageURL(3): `<html><head><title>Log in - Example Forum</title>` +
			`<meta name="generator" content="Discourse 2026.10.0"></head><body><h1>Log in</h1></body></html>`}
		h, _ := site.harvester(t, &browserSpyConverter{}, browserOff())
		result := h.FetchWithOptions(context.Background(), discourseTopicURL, FetchOptions{Refresh: true})
		for _, want := range []string{
			"5 of 7 posts loaded",
			"the posts after #6 are not loaded (next page, page 3)",
			`titled "Log in - Example Forum"`,
		} {
			if !strings.Contains(result.Partial, want) {
				t.Fatalf("the partial marker lacks %q: %q", want, result.Partial)
			}
		}
	})
	t.Run("the count unread", func(t *testing.T) {
		site := newDiscourseSite(7)
		site.status = map[string]int{"/t/placeholder-topic/4242.json": http.StatusForbidden, discoursePageURL(2): 500}
		h, _ := site.harvester(t, &browserSpyConverter{}, browserOff())
		result := h.FetchWithOptions(context.Background(), discourseTopicURL, FetchOptions{Refresh: true})
		for _, want := range []string{"3 posts loaded, the stated count not read", "HTTP 403", "HTTP 500"} {
			if !strings.Contains(result.Partial, want) {
				t.Fatalf("the partial marker lacks %q: %q", want, result.Partial)
			}
		}
		if !strings.Contains(result.Content, "**Posts:** count not read (the topic's JSON was not fetched") {
			t.Fatalf("an unread count renders as absence:\n%.700s", result.Content)
		}
	})
	t.Run("the cap", func(t *testing.T) {
		site := newDiscourseSite(loaderRequestCap + 1)
		site.pages = nil
		for number := 1; number <= loaderRequestCap+1; number++ {
			site.pages = append(site.pages, []discoursePost{{number, "poster_placeholder", "<p>A post.</p>", 0}})
		}
		h, _ := site.harvester(t, &browserSpyConverter{}, browserOn())
		result := h.FetchWithOptions(context.Background(), discourseTopicURL, FetchOptions{Refresh: true})
		if result.Error != "" || result.Method != rungDirect {
			t.Fatalf(
				"a capped topic was not kept: method=%q rungs=%v error=%q",
				result.Method,
				result.Rungs,
				result.Error,
			)
		}
		if loaderRequests := len(site.requests) - 1; loaderRequests != loaderRequestCap {
			t.Fatalf("%d loader requests, want the cap of %d", loaderRequests, loaderRequestCap)
		}
		for _, want := range []string{
			fmt.Sprintf("the cap of %d loader requests", loaderRequestCap),
			fmt.Sprintf("%d of %d posts loaded", loaderRequestCap, loaderRequestCap+1),
			fmt.Sprintf("next page, page %d)", loaderRequestCap+1),
		} {
			if !strings.Contains(result.Partial, want) {
				t.Fatalf("the partial marker lacks %q: %q", want, result.Partial)
			}
		}
	})
}

// TestDiscourseSinglePostPageLoadsTheWholeTopic: a post's own address serves
// that post alone, with no page link; the topic's first page is followed from
// it, then every next page, and the whole topic renders in number order.
func TestDiscourseSinglePostPageLoadsTheWholeTopic(t *testing.T) {
	site := newDiscourseSite(7)
	h, _ := site.harvester(t, &browserSpyConverter{}, browserOff())
	result := h.FetchWithOptions(context.Background(), discourseTopicURL+"/6", FetchOptions{Refresh: true})
	if result.Error != "" || result.Partial != "" {
		t.Fatalf("the topic from a post's address is not complete: partial=%q error=%q", result.Partial, result.Error)
	}
	if got := discoursePostHeaders(result.Content); strings.Join(got, "\n") != strings.Join(discourseAllPosts, "\n") {
		t.Fatalf("posts or their order are wrong:\n got %q\nwant %q", got, discourseAllPosts)
	}
}

// TestFollowedLoaderAnswersAreReplayedIntoALaterConversion: a second
// conversion of the same fetch (the next rung's page) gets every answer the
// first one followed replayed into its own page — none is requested again,
// and none is dropped from it.
func TestFollowedLoaderAnswersAreReplayedIntoALaterConversion(t *testing.T) {
	site := newDiscourseSite(7)
	h, _ := site.harvester(t, &browserSpyConverter{}, browserOff())
	page, err := url.Parse(discourseTopicURL)
	if err != nil {
		t.Fatal(err)
	}
	extractor := siteExtractors[len(siteExtractors)-1]
	if extractor.name != "discourse-topic" {
		t.Fatalf("the last registered extractor is %q, not the Discourse one", extractor.name)
	}
	budget := newLoaderBudget(context.Background())
	var rendered []string
	for conversion := range 2 {
		doc, err := html.Parse(bytes.NewReader([]byte(discourseCrawlerPage(site.pages, 1, true))))
		if err != nil {
			t.Fatal(err)
		}
		h.followLoaders(context.Background(), doc, page, extractor, budget)
		extraction, ok := extractDiscourseTopic(doc, page)
		if !ok {
			t.Fatalf("conversion %d: the page is not read as a topic", conversion)
		}
		rendered = append(rendered, extraction.markdown)
		if got := discoursePostHeaders(extraction.markdown); len(got) != 7 || extraction.partial != "" {
			t.Fatalf("conversion %d holds %d posts, partial %q:\n%s", conversion, len(got), extraction.partial,
				extraction.markdown)
		}
	}
	if len(site.requests) != 3 {
		t.Fatalf("%d requests over two conversions, want the 3 of the first: %v", len(site.requests), site.requests)
	}
	if rendered[0] != rendered[1] {
		t.Fatalf("the replayed conversion differs:\n%s\n---\n%s", rendered[0], rendered[1])
	}
}

// TestDiscourseUnreadCountFlagsTheTopicPartial: every page of the topic loads,
// but its stated count is not read — its JSON refused, or answered without a
// posts_count. Without the count, posts no page links cannot be ruled out, so
// the artifact is partial and names why; a page naming no topic address, whose
// count could never be requested, is named apart from one whose read failed.
func TestDiscourseUnreadCountFlagsTheTopicPartial(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(site *discourseSite)
		want  []string
		// requested is whether the topic's JSON was asked for.
		requested bool
	}{
		{
			"the JSON refused",
			func(site *discourseSite) {
				site.status = map[string]int{"/t/placeholder-topic/4242.json": http.StatusInternalServerError}
			},
			[]string{
				"7 posts loaded, the stated count not read", "the stated post count was not read",
				"the topic's post count", "HTTP 500",
			},
			true,
		},
		{
			"the JSON without posts_count",
			func(site *discourseSite) { site.countJSON = `{"id":4242,"post_stream":{"posts":[]}}` },
			[]string{"the stated post count was not read", "not the topic's JSON"},
			true,
		},
		{
			"no topic address",
			func(site *discourseSite) { site.anonymous, site.pages = true, site.pages[:1] },
			[]string{"3 posts loaded, the stated count not read", "could not be requested"},
			false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			site := newDiscourseSite(7)
			tc.setup(site)
			h, _ := site.harvester(t, &browserSpyConverter{}, browserOff())
			result := h.FetchWithOptions(context.Background(), discourseTopicURL, FetchOptions{Refresh: true})
			if result.Error != "" {
				t.Fatalf("fetch failed: %q", result.Error)
			}
			posts := 0
			for _, page := range site.pages {
				posts += len(page)
			}
			if got := len(discoursePostHeaders(result.Content)); got != posts {
				t.Fatalf("%d posts loaded, want every page's %d:\n%.700s", got, posts, result.Content)
			}
			for _, want := range tc.want {
				if !strings.Contains(result.Partial, want) {
					t.Fatalf("an unread count left the partial marker without %q: %q", want, result.Partial)
				}
			}
			requested := strings.Contains(strings.Join(site.requests, "\n"), ".json")
			if requested != tc.requested {
				t.Fatalf("the topic's JSON requested = %v, want %v: %v", requested, tc.requested, site.requests)
			}
		})
	}
}

// TestDiscourseAnswersNotProvedThisTopicAreNotMerged: a next page whose request
// a redirect took to another host is refused as off the site, and a page of
// unknown identity (no topic address on either side) merges nothing — neither
// grafts a foreign topic's posts in as this one's; each is named.
func TestDiscourseAnswersNotProvedThisTopicAreNotMerged(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(site *discourseSite)
		want  string
	}{
		{
			"redirected off the page's host",
			func(site *discourseSite) {
				site.redirects = map[string]string{
					discourseTopicURL + "?page=2": "https://foreign.example.test" + discoursePageURL(2),
				}
			},
			"answered from off the site (https://foreign.example.test/t/placeholder-topic/4242)",
		},
		{
			"of unknown identity",
			func(site *discourseSite) { site.anonymous = true },
			"not verifiable as this topic",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			site := newDiscourseSite(7)
			tc.setup(site)
			h, _ := site.harvester(t, &browserSpyConverter{}, browserOff())
			result := h.FetchWithOptions(context.Background(), discourseTopicURL, FetchOptions{Refresh: true})
			if result.Error != "" {
				t.Fatalf("fetch failed: %q", result.Error)
			}
			if got := discoursePostHeaders(result.Content); len(got) != 3 {
				t.Fatalf("posts of an unproved answer were merged: %q\n%.700s", got, result.Content)
			}
			if !strings.Contains(result.Partial, tc.want) ||
				!strings.Contains(result.Partial, "the posts after #3 are not loaded") {
				t.Fatalf("the refused answer is not named: %q", result.Partial)
			}
		})
	}
}

// TestDiscourseLoopingNextPagesEndBounded: a next page linking back to the
// first page, and a page linking itself as next, each end after one request
// per address — the answer holding no new post is a named failure, never a
// loop.
func TestDiscourseLoopingNextPagesEndBounded(t *testing.T) {
	loopTo := func(site *discourseSite, page, next int) string {
		return strings.ReplaceAll(discourseCrawlerPage(site.pages, page, true),
			`href="`+discoursePageURL(page+1)+`"`, `href="`+discoursePageURL(next)+`"`)
	}
	for _, tc := range []struct {
		name     string
		setup    func(site *discourseSite)
		requests int
	}{
		{"back to the first page", func(site *discourseSite) {
			site.answers = map[string]string{discoursePageURL(2): loopTo(site, 2, 1)}
		}, 4},
		{"to itself", func(site *discourseSite) {
			site.answers = map[string]string{discoursePageURL(1): loopTo(site, 1, 1)}
		}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			site := newDiscourseSite(7)
			tc.setup(site)
			h, _ := site.harvester(t, &browserSpyConverter{}, browserOff())
			result := h.FetchWithOptions(context.Background(), discourseTopicURL, FetchOptions{Refresh: true})
			if result.Error != "" || len(site.requests) != tc.requests {
				t.Fatalf("the loop was not bounded: requests=%v error=%q", site.requests, result.Error)
			}
			if !strings.Contains(result.Partial, "holding no post not already loaded") {
				t.Fatalf("the looping link is not named: %q", result.Partial)
			}
		})
	}
}

// discourseConvertTwice converts the topic's first page, then second — as
// the ladder converts a page once per rung — with one loader budget, returning
// each conversion's partial reason.
func discourseConvertTwice(
	t *testing.T,
	site *discourseSite,
	budget *loaderBudget,
	second string,
) (string, string) {
	t.Helper()
	h, _ := site.harvester(t, &browserSpyConverter{}, browserOff())
	var reasons []string
	for _, body := range []string{discourseCrawlerPage(site.pages, 1, true), second} {
		converted, _, err := h.convertHTML(context.Background(), discourseTopicURL, []byte(body), budget)
		if err != nil {
			t.Fatalf("conversion failed: %v", err)
		}
		reasons = append(reasons, partialReason(converted))
	}
	return reasons[0], reasons[1]
}

// TestDiscourseAnswersALaterConversionCannotTakeAreNamed: a later conversion
// of the fetch gets the followed answers replayed from the budget, never
// requested again. An answer that would not graft into that page, and one the
// replay store's bound left out, are each named as followed-but-unreplayed —
// never as not followed, and never dropped silently.
func TestDiscourseAnswersALaterConversionCannotTakeAreNamed(t *testing.T) {
	t.Run("would not graft", func(t *testing.T) {
		site := newDiscourseSite(7)
		// The later page already holds page two's posts: its answer adds none.
		holding := [][]discoursePost{append(append([]discoursePost{}, site.pages[0]...), site.pages[1]...)}
		holding = append(holding, site.pages[1:]...)
		first, second := discourseConvertTwice(t, site, newLoaderBudget(context.Background()),
			discourseCrawlerPage(holding, 1, true))
		if first != "" || len(site.requests) != 3 {
			t.Fatalf("the first conversion: partial=%q requests=%v", first, site.requests)
		}
		if !strings.Contains(second, "next page (page 2) was followed, but its answer would not graft into "+
			"this page") || strings.Contains(second, "not followed") {
			t.Fatalf("the unreplayed answer is not named as followed: %q", second)
		}
	})
	t.Run("past the replay bound", func(t *testing.T) {
		site := newDiscourseSite(7)
		budget := newLoaderBudget(context.Background())
		budget.replayLimit = 256 // the count's JSON fits; a page does not
		first, second := discourseConvertTwice(t, site, budget, discourseCrawlerPage(site.pages, 1, true))
		if first != "" || len(site.requests) != 3 {
			t.Fatalf("the first conversion: partial=%q requests=%v", first, site.requests)
		}
		if !strings.Contains(second, "next page (page 2) was followed, but its answer was not kept for replay") {
			t.Fatalf("the answer left out of replay is not named: %q", second)
		}
		if strings.Contains(second, "stated count not read") {
			t.Fatalf("the count's answer, within the bound, was not replayed: %q", second)
		}
	})
}

// TestDiscourseUnparsableLikeCountRendersUnread: a like count stated but not a
// number renders as unread, never as no likes.
func TestDiscourseUnparsableLikeCountRendersUnread(t *testing.T) {
	site := newDiscourseSite(3)
	body := strings.Replace(discourseCrawlerPage(site.pages[:1], 1, true),
		`itemprop="userInteractionCount" content="12"`, `itemprop="userInteractionCount" content="twelve"`, 1)
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	page, err := url.Parse(discourseTopicURL)
	if err != nil {
		t.Fatal(err)
	}
	extraction, ok := extractDiscourseTopic(doc, page)
	want := "## #1 · opener_placeholder · 2026-03-01 10:01 UTC · likes unread\n"
	if !ok || !strings.Contains(extraction.markdown, want) {
		t.Fatalf("an unparsable like count is not rendered unread:\n%.600s", extraction.markdown)
	}
}
