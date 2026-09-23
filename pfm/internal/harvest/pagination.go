package harvest

import (
	"fmt"
	"net/url"
	"regexp"
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
// query (?page=2, &start=20), or the path — less any page suffix of its own —
// with a page segment (/2/, /page/2/, /page-2); a blog's link to the
// adjacent post is another page, not this one continued.

// paginationSegmentRe is a trailing page segment of a path: /2, /page/2,
// /page-2, with or without a trailing slash.
var paginationSegmentRe = regexp.MustCompile(`(?i)/(?:page[-/]?)?\d+/?$`)

// paginationQueryKeys are the query keys a continuation label shows with
// their value; any other key's value is not repeated (it may be a token).
var paginationQueryKeys = map[string]bool{
	"page": true, "p": true, "pg": true, "paged": true, "start": true,
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
		return target.RawQuery != page.RawQuery
	}
	base := paginationSegmentRe.ReplaceAllString(pagePath, "")
	rest, ok := strings.CutPrefix(targetPath, base)
	return ok && rest != "" && paginationSegmentRe.FindString(rest) == rest
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
