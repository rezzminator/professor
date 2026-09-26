package harvest

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// reviewItems is n schema.org Review items as a JSON-LD review array.
func reviewItems(n int) string {
	items := make([]string, n)
	for i := range items {
		items[i] = fmt.Sprintf(`{"@type":"Review","reviewBody":"review %d"}`, i)
	}
	return strings.Join(items, ",")
}

// reviewBlocks is n review elements carrying their review's id, as a review
// list renders them.
func reviewBlocks(n int) string {
	var blocks strings.Builder
	for i := range n {
		fmt.Fprintf(&blocks, `<li id="empReview_%d"><h2>Review %d</h2><p>Pros and cons.</p></li>`, 9744160+i, i)
	}
	return blocks.String()
}

// lobstersComments is n comments as a Lobsters thread renders them (captured
// markup, text scrubbed): a folder input and a comment div, each id carrying
// the comment's short id — c_ on the comment, no noun on most of it.
func lobstersComments(n int) string {
	var comments strings.Builder
	for i := range n {
		short := "0" + strconv.FormatInt(int64(46656+i*37), 36)
		fmt.Fprintf(&comments, `<li class="comments_subtree">`+
			`<input id="comment_folder_%s" class="comment_folder_button" type="checkbox">`+
			`<div id="c_%s" class="comment"><div class="comment_text"><p>Comment %d.</p></div></div></li>`, short, short, i)
	}
	return comments.String()
}

// fetchGenericPage harvests page served at source on the generic path.
func fetchGenericPage(t *testing.T, source, page string) Result {
	t.Helper()
	served := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() == source {
			return response(request, http.StatusOK, "text/html; charset=UTF-8", page), nil
		}
		return response(request, http.StatusNotFound, "text/html", "<html><body>not found</body></html>"), nil
	})
	converter := &browserSpyConverter{
		convertFn: func(_ context.Context, _, _ string, body []byte) (string, error) {
			text := scriptRe.ReplaceAllString(string(body), " ")
			text = htmlTagRe.ReplaceAllString(styleRe.ReplaceAllString(text, " "), " ")
			return strings.Join(strings.Fields(text), " "), nil
		},
	}
	h := mustNew(t, Options{
		CacheDir:    t.TempDir(),
		Client:      &http.Client{Transport: served},
		Chrome:      &http.Client{Transport: served},
		Jina:        &http.Client{Transport: served},
		OA:          &http.Client{Transport: served},
		Converter:   converter,
		BrowserRung: browserOff(),
		Clock:       newPacingClock(),
	})
	result := h.FetchWithOptions(context.Background(), source, FetchOptions{Refresh: true})
	if result.Error != "" {
		t.Fatalf("the page failed: %s", result.Error)
	}
	return result
}

// TestStatedCountsAgainstLoaded: a generic page stating how many reviews,
// comments or answers it has — in its structured data, or in a count label
// tied to the list it shows — and carrying far fewer is stored partial,
// stated against loaded; a page carrying what it states, and a listing whose
// counters belong to its cards or to the site, are not flagged.
func TestStatedCountsAgainstLoaded(t *testing.T) {
	const source = "https://jobs.example.com/Reviews/Example-Reviews-E1234.htm"
	head := func(jsonLD string) string {
		return `<html><head><script type="application/ld+json">` + jsonLD + `</script></head><body><article>` +
			articlePreview + `</article>`
	}
	for _, tc := range []struct {
		name, page, partial string
	}{
		{
			name: "JSON-LD stating 103 reviews and carrying 1",
			page: head(`{"@context":"https://schema.org","@type":"Organization","name":"Example",`+
				`"aggregateRating":{"@type":"AggregateRating","ratingValue":"4.4","reviewCount":"103"},"review":[`+
				reviewItems(1)+`]}`) + `<h2>103 reviews</h2></body></html>`,
			partial: "103 reviews stated · 1 loaded — the rest are not on the served page (paged or loaded on demand)",
		},
		{
			name: "JSON-LD stating 3 reviews and carrying 3",
			page: head(`{"@type":"Organization","aggregateRating":{"@type":"AggregateRating","reviewCount":3},`+
				`"review":[`+reviewItems(3)+`]}`) + `<h2>3 reviews</h2></body></html>`,
		},
		{
			name: "a question stating 12 answers and carrying 2",
			page: head(`{"@type":"QAPage","mainEntity":{"@type":"Question","name":"Why?","answerCount":12,`+
				`"acceptedAnswer":{"@type":"Answer","text":"one"},"suggestedAnswer":[{"@type":"Answer","text":"two"}]}}`) +
				`</body></html>`,
			partial: "12 answers stated · 2 loaded",
		},
		{
			name: "a localized count label over a review list holding 1 of 103",
			page: `<html lang="nl"><body><article>` + articlePreview + `</article><h2>103 reviews</h2><ol>` +
				reviewBlocks(1) + `</ol></body></html>`,
			partial: "103 reviews stated · 1 loaded — the rest are not on the served page",
		},
		{
			name: "a count label over a list holding every review",
			page: `<html><body><article>` + articlePreview + `</article><h2>4 reviews</h2><ol>` + reviewBlocks(4) +
				`</ol></body></html>`,
		},
		{
			name: "a count label over a thread whose comment ids are letters and digits",
			page: `<html><body><article>` + articlePreview + `</article><h2>6 comments</h2><section>` +
				`<article id="comment-2k3j"><p>One.</p></article><article id="comment-abcd"><p>Two.</p></article>` +
				`<article id="comment-9xyz"><p>Three.</p></article><article id="comment-wxyz"><p>Four.</p></article>` +
				`<article id="comment-qrst"><p>Five.</p></article><article id="comment-mnop"><p>Six.</p></article>` +
				`</section></body></html>`,
		},
		{
			name: "a thread holding every comment under c_ ids beside one chrome id naming comments",
			page: `<html><body><article>` + articlePreview + `</article><a id="comments-mroowi" href="#c">` +
				`199 comments</a><ol>` + lobstersComments(199) + `</ol></body></html>`,
		},
		{
			name: "a listing whose cards and site state their own counters",
			page: `<html><body><header><p>10M users</p><p>Over 2,000,000 reviews</p></header><article>` +
				articlePreview + `</article><ul>` +
				`<li id="company-1"><a href="/c/1">Example One</a><span>(12 reviews)</span></li>` +
				`<li id="company-2"><a href="/c/2">Example Two</a><span>(340 reviews)</span></li>` +
				`<li id="company-3"><a href="/c/3">Example Three</a><span>(5,210 reviews)</span></li>` +
				`</ul></body></html>`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := fetchGenericPage(t, source, tc.page)
			switch {
			case tc.partial == "" && result.Partial != "":
				t.Fatalf("a page with no stated gap was flagged partial: %q", result.Partial)
			case tc.partial != "" && !strings.Contains(result.Partial, tc.partial):
				t.Fatalf("partial = %q, want it to name %q", result.Partial, tc.partial)
			}
		})
	}
}

// TestStatedCountsLeaveReconciledExtractorPagesAlone: a page a site extractor
// rendered and reconciled keeps its own count line; a stated count in its
// markup is never read again by the generic check.
func TestStatedCountsLeaveReconciledExtractorPagesAlone(t *testing.T) {
	page := strings.Replace(hnFixture(t), "</head>", `<script type="application/ld+json">`+
		`{"@type":"DiscussionForumPosting","commentCount":999}</script></head>`, 1)
	page = strings.Replace(page, "<body>", "<body><h2>999 comments</h2>", 1)
	site := &hnSite{pages: []string{page}}
	h, _ := site.harvester(t)
	result := h.FetchWithOptions(context.Background(), hnThreadURL, FetchOptions{Refresh: true})
	if result.Error != "" || result.Partial != "" {
		t.Fatalf("the reconciled thread was flagged: partial=%q error=%q", result.Partial, result.Error)
	}
	if !strings.Contains(result.Content, "**Comments:** 53 stated · 53 loaded") {
		t.Fatalf("the extractor's count line is gone:\n%.1500s", result.Content)
	}
}
