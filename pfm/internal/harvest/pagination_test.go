package harvest

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// TestAnUnfollowedNextPageFlagsAGenericPagePartial: a page no extractor
// claims links its own next page. The artifact is flagged partial naming the
// continuation — never silently the first page alone — and no browser render
// is spent on it (a render is the same page). A rel="next" to a different
// complete page — a blog's adjacent post, the next post id, a numbered sibling
// — is not a continuation, nor is a link differing only in a query key that
// does not page.
func TestAnUnfollowedNextPageFlagsAGenericPagePartial(t *testing.T) {
	article := strings.Repeat("A long paragraph of the article's own text, with enough words to count. ", 20)
	for _, tc := range []struct {
		name, source, next, gap string
	}{
		{"query page", "https://blog.example.test/guide", "/guide?page=2&session=s3cr3t", "guide?page=2&session=…"},
		{"path page", "https://blog.example.test/guide/", "/guide/2/", "https://blog.example.test/guide/2/"},
		{"second page", "https://blog.example.test/threads/t.12/page-2", "/threads/t.12/page-3", "page-3"},
		{"listing page", "https://blog.example.test/blog/", "/blog/page/2/", "https://blog.example.test/blog/page/2/"},
		{"listing second page", "https://blog.example.test/blog/page/2/", "/blog/page/3/", "blog/page/3/"},
		{"a post's second page", "https://blog.example.test/archives/145", "/archives/145/2", "archives/145/2"},
		{"adjacent post", "https://blog.example.test/2026/03/guide/", "/2026/03/another-guide/", ""},
		{"numbered sibling", "https://blog.example.test/archives/145", "/archives/146", ""},
		{"numbered chapter", "https://blog.example.test/docs/3/", "/docs/4/", ""},
		{"next lesson", "https://blog.example.test/course/lesson/1", "/course/lesson/2", ""},
		{"next post id", "https://blog.example.test/?p=145", "/?p=146", ""},
		{"next photo", "https://blog.example.test/gallery?photo=101", "/gallery?photo=102", ""},
		{"tracking only", "https://blog.example.test/guide", "/guide?utm_source=feed", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := `<html><head><title>Guide</title><link rel="next" title="Next" href="` + tc.next + `"></head>` +
				`<body><main><h1>Guide</h1><p>` + article + `</p></main></body></html>`
			spy := &browserSpyConverter{html: page, status: http.StatusOK, convertFn: (&fakeConverter{}).Convert}
			h := pageHarvester(t, page, spy, browserOn())
			result := h.Fetch(context.Background(), tc.source)
			if result.Error != "" || result.Method != rungDirect || spy.browserCalls != 0 {
				t.Fatalf("page not kept at the direct rung: method=%q rungs=%v browser=%d error=%q",
					result.Method, result.Rungs, spy.browserCalls, result.Error)
			}
			if tc.gap == "" {
				if result.Partial != "" {
					t.Fatalf("a link to another page flagged the artifact partial: %q", result.Partial)
				}
				return
			}
			if !strings.Contains(result.Partial, `rel="next"`) || !strings.Contains(result.Partial, tc.gap) ||
				strings.Contains(result.Partial, "s3cr3t") {
				t.Fatalf("the continuation is not named (or leaks a query value): %q", result.Partial)
			}
		})
	}
}

// TestANumberedPaginatorNamesTheNextPage: a page that links its own path's
// later pages by number (/page/N, ?page=N) with no rel="next" names the next
// one. A list of links to other documents with numeric ids, or WordPress's
// post ids, is not a paginator.
func TestANumberedPaginatorNamesTheNextPage(t *testing.T) {
	for _, tc := range []struct {
		name, source, gap string
		links             []string
	}{
		{
			name:   "tag page one",
			source: "https://forum.example.test/t/go",
			gap:    "https://forum.example.test/t/go/page/2",
			links:  []string{"/t/go/page/2", "/t/go/page/3", "/t/go/page/850"},
		},
		{
			name:   "tag page two",
			source: "https://forum.example.test/t/go/page/2",
			gap:    "https://forum.example.test/t/go/page/3",
			links:  []string{"/t/go", "/t/go/page/3", "/t/go/page/850"},
		},
		{
			name:   "query pages",
			source: "https://forum.example.test/search?q=go&page=4",
			gap:    "search?q=…&page=5",
			links:  []string{"/search?q=go&page=3", "/search?q=go&page=5", "/search?q=go&page=9"},
		},
		{
			name:   "article ids",
			source: "https://blog.example.test/articles",
			links:  []string{"/articles/1041", "/articles/1042", "/articles/1043", "/articles/1044"},
		},
		{
			name:   "post ids",
			source: "https://blog.example.test/?p=145",
			links:  []string{"/?p=146", "/?p=147", "/?p=148"},
		},
		{
			name:   "another path's pages",
			source: "https://forum.example.test/t/go",
			links:  []string{"/t/rust/page/2", "/t/rust/page/3"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body strings.Builder
			body.WriteString(`<html><body><main><p>Listing</p><nav>`)
			for _, link := range tc.links {
				body.WriteString(`<a href="` + link + `">` + link + `</a> `)
			}
			body.WriteString(`</nav></main></body></html>`)
			doc, err := html.Parse(strings.NewReader(body.String()))
			if err != nil {
				t.Fatalf("parse the fixture: %v", err)
			}
			page, err := url.Parse(tc.source)
			if err != nil {
				t.Fatalf("parse %q: %v", tc.source, err)
			}
			got := paginationContinuation(doc, page)
			if tc.gap == "" {
				if got != "" {
					t.Fatalf("links to other documents named as a next page: %q", got)
				}
				return
			}
			if !strings.Contains(got, tc.gap) {
				t.Fatalf("the numbered next page %q is not named: %q", tc.gap, got)
			}
		})
	}
}

// readerSite is a site whose origin answers origin (nil: every request fails
// in transport) and whose Jina reader answers markdown.
func readerSite(
	t *testing.T,
	origin func(*http.Request) *http.Response,
	markdown string,
	browser *browserSpyConverter,
) *Harvester {
	t.Helper()
	site := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "defuddle.md" || origin == nil {
			return nil, errors.New("connection refused")
		}
		return origin(request), nil
	})
	jina := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusOK, "text/markdown", markdown), nil
	})
	missing := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusNotFound, "application/json", `{}`), nil
	})
	var converter Converter = &fakeConverter{}
	rung := browserOff()
	if browser != nil {
		browser.convertFn = tagStripConverter().Convert
		converter, rung = browser, browserOn()
	}
	return mustNew(t, Options{
		CacheDir:    t.TempDir(),
		Client:      &http.Client{Transport: site},
		Chrome:      &http.Client{Transport: site},
		Jina:        &http.Client{Transport: canonicalReader(jina)},
		OA:          &http.Client{Transport: missing},
		Converter:   converter,
		BrowserRung: rung,
		Clock:       newPacingClock(),
	})
}

var listingMarkdown = "# Questions tagged go\n\n" + strings.Repeat(
	"- [How do I read a file line by line?](https://qa.example.test/questions/101/read-lines) 42 votes\n", 20)

// TestAReaderRungNamesTheNextPage: the reader rungs store markdown the HTML
// guard never reads. A next page learned from HTML the ladder refused (a 403
// listing) is carried into the reader's partial; with no HTML seen, the
// reader's own markdown names it — a Next link or the page number plus one —
// under the HTML guard's exclusions.
func TestAReaderRungNamesTheNextPage(t *testing.T) {
	const listing = "https://qa.example.test/questions/tagged/go?tab=votes&page=2&pagesize=50"
	const pageThree = "https://qa.example.test/questions/tagged/go?tab=votes&page=3&pagesize=50"
	t.Run("carried from a refused page", func(t *testing.T) {
		refused := `<html><head><link rel="next" href="/questions/tagged/go?tab=votes&page=3&pagesize=50"></head>` +
			`<body><main><h1>Questions tagged go</h1><p>` + strings.Repeat("A question title with its votes. ", 40) +
			`</p></main></body></html>`
		h := readerSite(t, func(request *http.Request) *http.Response {
			return response(request, http.StatusForbidden, "text/html", refused)
		}, listingMarkdown, nil)
		result := h.Fetch(context.Background(), listing)
		if result.Error != "" || result.Method != "jina" {
			t.Fatalf("listing not stored by the reader: method=%q rungs=%v error=%q",
				result.Method, result.Rungs, result.Error)
		}
		if !strings.Contains(result.Partial, "page=3") {
			t.Fatalf("the refused page's next page is not carried into the reader's partial: %q", result.Partial)
		}
	})
	for _, tc := range []struct {
		name, source, link, gap string
	}{
		{"a Next link", listing, "[Next](" + pageThree + ")", "page=3"},
		{"a numbered link", listing, "[3](" + pageThree + ` "Go to page 3")`, "page=3"},
		{"a › link", "https://forum.example.test/t/go", "[›](https://forum.example.test/t/go/page/2)", "t/go/page/2"},
		{"next post id", "https://blog.example.test/?p=145", "[Next](https://blog.example.test/?p=146)", ""},
		{
			"numbered sibling", "https://blog.example.test/archives/145",
			"[Next »](https://blog.example.test/archives/146)", "",
		},
		{"a previous page", listing, "[1](" + strings.Replace(pageThree, "page=3", "page=1", 1) + ")", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := readerSite(t, nil, listingMarkdown+"\n"+tc.link+"\n", nil)
			result := h.Fetch(context.Background(), tc.source)
			if result.Error != "" || result.Method != "jina" {
				t.Fatalf("page not stored by the reader: method=%q rungs=%v error=%q",
					result.Method, result.Rungs, result.Error)
			}
			if tc.gap == "" {
				if result.Partial != "" {
					t.Fatalf("a link to another page flagged the reader's markdown partial: %q", result.Partial)
				}
				return
			}
			if !strings.Contains(result.Partial, tc.gap) {
				t.Fatalf("the reader's next page %q is not named: %q", tc.gap, result.Partial)
			}
		})
	}
}

// TestAHashRouteOfAnAppShellIsRenderedNeverRead: a reader or an archive is
// sent the address without its fragment, so for a hash route of an empty app
// shell it stores the app's home view. The ladder skips them; the browser
// renders the full address, #/route included.
func TestAHashRouteOfAnAppShellIsRenderedNeverRead(t *testing.T) {
	const source = "https://docs.example.test/#/quickstart"
	shell := `<!doctype html><html><head><title>docs</title></head><body><div id="app"></div>` +
		`<script src="//cdn.example.test/app.min.js"></script></body></html>`
	home := "# Home\n\n" + strings.Repeat("The project's home README: what it is, and why one would use it. ", 20)
	route := `<html><body><div id="app"><h1>Quick start</h1>` + strings.Repeat(
		`<p>Install the command line tool, initialise a docs directory and preview the site locally.</p>`, 8) +
		`</div></body></html>`
	spy := &browserSpyConverter{html: route, status: http.StatusOK}
	h := readerSite(t, func(request *http.Request) *http.Response {
		return response(request, http.StatusOK, "text/html", shell)
	}, home, spy)
	result := h.Fetch(context.Background(), source)
	if result.Error != "" || result.Method != "browser-chrome" || !strings.Contains(result.Content, "Quick start") {
		t.Fatalf("the hash route was not rendered: method=%q rungs=%v error=%q content=%.120q",
			result.Method, result.Rungs, result.Error, result.Content)
	}
	if len(spy.sources) == 0 || spy.sources[0] != source {
		t.Fatalf("the browser was not sent the full address: %v", spy.sources)
	}
	for _, rung := range result.Rungs {
		if rung == "jina" || rung == "defuddle" {
			t.Fatalf("a reader was sent the hash route's address less its fragment: rungs=%v", result.Rungs)
		}
	}
}

// TestAPagedAddressNamesItsUnfetchedPages: an address that names its own page
// (?page=2) is one page of a listing. When no rung saw a pager that answers
// whether a later page exists — a wall served in its place, a reader's
// markdown without one — the stored result names the pages it did not read.
// A pager showing this page as the last, or a WordPress post id (?p=2), names
// nothing.
func TestAPagedAddressNamesItsUnfetchedPages(t *testing.T) {
	const listing = "https://qa.example.test/questions/tagged/go?tab=votes&page=2&pagesize=50"
	wall := `<html><body><main><h1>Access denied</h1><p>` + strings.Repeat("Your request was blocked. ", 20) +
		`</p></main></body></html>`
	lastPage := `<html><body><main><h1>Questions tagged go</h1><p>` +
		strings.Repeat("A question title with its votes. ", 40) + `</p><nav>` +
		`<a href="/questions/tagged/go?tab=votes&page=1&pagesize=50">1</a> ` +
		`<a href="/questions/tagged/go?tab=votes&page=2&pagesize=50" aria-current="page">2</a>` +
		`</nav></main></body></html>`
	for _, tc := range []struct {
		name, source, origin, gap string
	}{
		{"a walled listing", listing, wall, "a paged listing (page 2); later pages were not read"},
		{"the last page shown", listing, lastPage, ""},
		{"a post id", "https://blog.example.test/?p=2", wall, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := readerSite(t, func(request *http.Request) *http.Response {
				return response(request, http.StatusForbidden, "text/html", tc.origin)
			}, listingMarkdown, nil)
			result := h.Fetch(context.Background(), tc.source)
			if result.Error != "" || result.Method != "jina" {
				t.Fatalf("page not stored by the reader: method=%q rungs=%v error=%q",
					result.Method, result.Rungs, result.Error)
			}
			if tc.gap == "" {
				if result.Partial != "" {
					t.Fatalf("a page whose pager was answered, or no page at all, was named: %q", result.Partial)
				}
				return
			}
			if !strings.Contains(result.Partial, tc.gap) {
				t.Fatalf("the paged listing's unread pages are not named: %q", result.Partial)
			}
		})
	}
}
