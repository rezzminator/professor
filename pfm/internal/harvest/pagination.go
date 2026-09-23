package harvest

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
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
// own address; "" when the page links none.
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
		return fmt.Sprintf(`the page links its next page (rel="next": %s), which was not followed — `+
			"only this page's content is in the artifact", paginationLabel(target))
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
