package harvest

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// The pagination guard. A page that links the next page of its own content —
// a paginated thread, article or listing: <link rel="next"> in its head, or an
// <a rel="next"> — advertises content its artifact does not hold. The generic
// path converts the one page it fetched; a site extractor that knows the
// pagination follows it (discourse.go). Every other page's continuation is
// named on the artifact as a partial reason, never dropped silently. Only a
// link continuing the page's OWN address counts: the same path with another
// value of a pagination key (?page=2, &start=20); the page's path with a page
// segment (/2/, /page/2/, /page-2); or, for a page naming its own page
// (/page/2), its path less that segment with another. A link to a
// complete page next to this one is another page, not this one continued: a
// blog's adjacent post, the same path with another post id (?p=146) — and a
// numbered sibling (/archives/145 → /archives/146, /docs/3/ → /docs/4/), since
// a bare trailing number is as often an id as a page, so /guide/2/ →
// /guide/3/ goes unflagged too.

var (
	// paginationSegmentRe is a trailing page segment of a path: /2, /page/2,
	// /page-2, with or without a trailing slash.
	paginationSegmentRe = regexp.MustCompile(`(?i)/(?:page[-/]?)?\d+/?$`)
	// paginationNamedSegmentRe is a trailing page segment that names itself
	// a page: /page/2, /page-2, /page2.
	paginationNamedSegmentRe = regexp.MustCompile(`(?i)/page[-/]?\d+$`)
)

// paginationQueryKeys are the query keys that page through content: a link
// differing from the page in one continues it, and a continuation label shows
// their value; any other key's value is not repeated (it may be a token). p
// is not one: it is WordPress's post id.
var paginationQueryKeys = map[string]bool{
	"page": true, "pg": true, "paged": true, "start": true,
	"offset": true, "from": true, "skip": true,
}

// paginationContinuation names doc's un-followed continuation for the partial
// marker: the reason naming the first rel="next" link that continues page's
// own address, else its numbered next page (numberedNext); "" when the page
// links none.
func paginationContinuation(doc *html.Node, page *url.URL) string {
	if page == nil || page.Host == "" {
		return ""
	}
	for _, href := range relTargets(doc, relNext) {
		parsed, err := url.Parse(href)
		if err != nil {
			continue
		}
		target := page.ResolveReference(parsed)
		target.Fragment = ""
		if !continuesPage(page, target) {
			continue
		}
		return continuationNote(`rel="next"`, target)
	}
	if next := numberedNext(page, anchorTargets(doc, page)); next != nil {
		return continuationNote("a numbered page link", next)
	}
	return ""
}

// continuesPage reports whether target is page's own address continued.
func continuesPage(page, target *url.URL) bool {
	if !strings.EqualFold(page.Hostname(), target.Hostname()) {
		return false
	}
	pagePath := strings.TrimSuffix(page.EscapedPath(), "/")
	targetPath := strings.TrimSuffix(target.EscapedPath(), "/")
	if targetPath == pagePath {
		return paginationKeyDiffers(page.Query(), target.Query())
	}
	if pageSegmentFollows(pagePath, targetPath) {
		return true
	}
	named := paginationNamedSegmentRe.FindString(pagePath)
	return named != "" && pageSegmentFollows(strings.TrimSuffix(pagePath, named), targetPath)
}

// pageSegmentFollows reports whether targetPath is base with one page segment.
func pageSegmentFollows(base, targetPath string) bool {
	rest, ok := strings.CutPrefix(targetPath, base)
	return ok && rest != "" && paginationSegmentRe.FindString(rest) == rest
}

// paginationKeyDiffers reports whether a pagination key's values differ
// between the two queries.
func paginationKeyDiffers(page, target url.Values) bool {
	for _, query := range []url.Values{page, target} {
		for key := range query {
			if paginationQueryKeys[strings.ToLower(key)] && !slices.Equal(page[key], target[key]) {
				return true
			}
		}
	}
	return false
}

// paginationLabel is target as a partial reason shows it: scheme, host and
// path, with only the pagination keys of its query.
func paginationLabel(target *url.URL) string {
	label := safeURL(target.String())
	if target.RawQuery == "" {
		return label
	}
	var kept []string
	for _, pair := range strings.Split(target.RawQuery, "&") {
		key, _, _ := strings.Cut(pair, "=")
		if paginationQueryKeys[strings.ToLower(key)] {
			kept = append(kept, pair)
		} else {
			kept = append(kept, key+"=…")
		}
	}
	return label + "?" + strings.Join(kept, "&")
}

// The numbered paginator. A page that links its own path's later pages by
// number — /page/N, ?page=N, &page=N past its own page's number — names the
// next one with no rel="next". Only a named page (a page segment or a paging
// key) counts: a bare trailing number is as often a document id, so a list of
// links to other articles (/articles/1041, /articles/1042) is no paginator,
// nor are WordPress's post ids (?p=146). The reader rungs' markdown is read
// the same way (markdownContinuation), plus a link labelled Next.

var (
	// paginationNumberRe captures the number of a trailing named page segment.
	paginationNumberRe = regexp.MustCompile(`(?i)/page[-/]?(\d+)/?$`)
	// markdownLinkRe is a markdown link: its label and its target.
	markdownLinkRe = regexp.MustCompile(`\[([^\]\n]*)\]\(\s*<?([^)\s>]+)>?(?:\s+"[^"\n]*")?\s*\)`)
)

// pageKey is the paging key most sites use.
const pageKey = "page"

// pageNumberKeys are the paging keys whose value is a page number.
var pageNumberKeys = map[string]bool{pageKey: true, "pg": true, "paged": true}

// nextLinkLabels are the labels of a link to the next page.
var nextLinkLabels = map[string]bool{
	"next": true, "next page": true, "›": true, "»": true, "next ›": true, "next »": true, "next >": true,
}

// continuationNote is the partial reason naming target, the page's next page,
// which how (the link that names it) shows.
func continuationNote(how string, target *url.URL) string {
	return fmt.Sprintf("the page links its next page (%s: %s), which was not followed — "+
		"only this page's content is in the artifact", how, paginationLabel(target))
}

// pageNumber is the page number u's address names — a named page segment or
// a paging key's value — or 0 when it names none.
func pageNumber(u *url.URL) int {
	if match := paginationNumberRe.FindStringSubmatch(u.EscapedPath()); match != nil {
		if n, err := strconv.Atoi(match[1]); err == nil {
			return n
		}
	}
	for key, values := range u.Query() {
		if !pageNumberKeys[strings.ToLower(key)] || len(values) != 1 {
			continue
		}
		if n, err := strconv.Atoi(values[0]); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

// numberedNext is the lowest-numbered of targets that continues page's own
// address past its page number; nil when none does.
func numberedNext(page *url.URL, targets []*url.URL) *url.URL {
	current := max(pageNumber(page), 1)
	var next *url.URL
	nextNumber := 0
	for _, target := range targets {
		number := pageNumber(target)
		if number > current && (next == nil || number < nextNumber) && continuesPage(page, target) {
			next, nextNumber = target, number
		}
	}
	return next
}

// anchorTargets are the addresses doc's links resolve to against page.
func anchorTargets(doc *html.Node, page *url.URL) []*url.URL {
	var targets []*url.URL
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "a" {
			if parsed, err := url.Parse(strings.TrimSpace(nodeAttr(node, "href"))); err == nil {
				target := page.ResolveReference(parsed)
				target.Fragment = ""
				targets = append(targets, target)
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return targets
}

// markdownContinuation names a reader rung's markdown's un-followed next page
// for the partial marker: a link labelled Next that continues page's own
// address, or its numbered next page; "" when it links none.
func markdownContinuation(markdown string, page *url.URL) string {
	labels, targets := markdownLinks(markdown, page)
	for i, target := range targets {
		if nextLinkLabels[labels[i]] && continuesPage(page, target) {
			return continuationNote("a Next link", target)
		}
	}
	if next := numberedNext(page, targets); next != nil {
		return continuationNote("a numbered page link", next)
	}
	return ""
}

// markdownLinks are markdown's links: each one's label, lowercased, and the
// address it resolves to against page.
func markdownLinks(markdown string, page *url.URL) (labels []string, targets []*url.URL) {
	for _, match := range markdownLinkRe.FindAllStringSubmatch(markdown, -1) {
		parsed, err := url.Parse(match[2])
		if err != nil {
			continue
		}
		target := page.ResolveReference(parsed)
		target.Fragment = ""
		labels = append(labels, strings.ToLower(strings.Join(strings.Fields(strings.Trim(match[1], "*_` ")), " ")))
		targets = append(targets, target)
	}
	return labels, targets
}

// The paged address. An address naming its own page (?page=2, /page/2) is one
// page of a listing, the pages after it unread. Its next page is named when a
// rung saw it; when no rung saw a pager at all — a wall served in its place, a
// reader's markdown without one — the listing's later pages are named instead
// (pagedListing). A pager that shows this page as its last answers the
// question, and nothing is named.

// pagedListing is the partial reason naming the unread later pages of the
// page source's address names; "" when the address names none.
func pagedListing(source string) string {
	page, err := url.Parse(source)
	if err != nil || page.Host == "" {
		return ""
	}
	if number := pageNumber(page); number > 0 {
		return fmt.Sprintf("a paged listing (page %d); later pages were not read", number)
	}
	return ""
}

// pagerShown reports whether targets hold a pager of page's own address: a
// link to another numbered page of it, which answers whether a next page
// exists.
func pagerShown(page *url.URL, targets []*url.URL) bool {
	for _, target := range targets {
		if pageNumber(target) > 0 && continuesPage(page, target) {
			return true
		}
	}
	return false
}

// htmlPagerShown reports whether doc shows a pager of page's own address: a
// rel="prev" or rel="next" link continuing it, or a numbered page link.
func htmlPagerShown(doc *html.Node, page *url.URL) bool {
	targets := anchorTargets(doc, page)
	for _, rel := range []string{relPrev, relNext} {
		for _, href := range relTargets(doc, rel) {
			if parsed, err := url.Parse(href); err == nil && continuesPage(page, page.ResolveReference(parsed)) {
				return true
			}
		}
	}
	return pagerShown(page, targets)
}
