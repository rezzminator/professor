package harvest

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// A rung asks for one address and may be answered from another: the origin
// redirected it (an HTTP rung's redirect chain, the browser's final location),
// or the reader rendered wherever the site sent it (the reader's page names
// its canonical address). A redirect to the same page — http to https, a
// trailing slash, www, a locale prefix, tracking parameters, a slug added to
// the address, a same-host permanent move keeping the page's name
// (movedPage) — is the page. A redirect to another page of the site (a
// search, a listing) is stored under the requested address only with that
// named; a redirect to a login or the site's home page is no page at all
// (carriedGaps.refused). A rung that cannot tell where it landed says so,
// never reads as no redirect.

// fetchPage is the one request getBodyWithHeaders makes, with the address the
// answer came from (final).
func fetchPage(
	ctx context.Context,
	client *http.Client,
	rawURL, ua string,
	headers map[string]string,
	maxBytes int64,
) (body []byte, status int, contentType, final string, err error) {
	response, _, err := fetchPageHops(ctx, client, rawURL, ua, headers, maxBytes)
	return response.body, response.status, response.contentType, response.finalURL, err
}

// fetchPageHops is fetchPage's answer with the status of every redirect it came
// through, in order: the client's own redirect policy still decides each hop
// (gatewayClient chains it).
func fetchPageHops(
	ctx context.Context,
	client *http.Client,
	rawURL, ua string,
	headers map[string]string,
	maxBytes int64,
) (response gatewayResponse, hops []int, err error) {
	header := make(http.Header, len(headers))
	for key, value := range headers {
		header.Set(key, value)
	}
	var recording http.Client // a copy: the shared client's policy is never rewritten
	if client != nil {
		recording = *client
	}
	policy := recording.CheckRedirect
	recording.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if next.Response != nil {
			hops = append(hops, next.Response.StatusCode)
		}
		if policy != nil {
			return policy(next, via)
		}
		return nil
	}
	response, err = gatewayAttempt(ctx, gatewayRequest{
		url:              rawURL,
		client:           &recording,
		ua:               ua,
		headers:          header,
		max:              maxBytes,
		oversizeTruncate: true,
	})
	return response, hops, err
}

// movedPage reports whether final is the requested page moved: every redirect
// hop was permanent (301 or 308), the host is the same (www aside), and the
// landing keeps the requested address's last path segment (its file or slug
// name, an index document aside). A temporary hop, another host or another
// name is not evidence of a move.
func movedPage(requested, final string, hops []int) bool {
	if len(hops) == 0 {
		return false
	}
	for _, hop := range hops {
		if hop != http.StatusMovedPermanently && hop != http.StatusPermanentRedirect {
			return false
		}
	}
	want, wantErr := url.Parse(requested)
	got, gotErr := url.Parse(final)
	if wantErr != nil || gotErr != nil {
		return false
	}
	host := func(address *url.URL) string {
		return strings.TrimPrefix(strings.ToLower(address.Hostname()), "www.")
	}
	last := func(address *url.URL) string {
		path := landingPath(address)
		return path[strings.LastIndex(path, "/")+1:]
	}
	return host(want) == host(got) && last(want) != "" && last(want) == last(got)
}

// landingKind is what a rung's final address is to the requested page.
type landingKind int

const (
	landedSame      landingKind = iota // the requested page
	landedUnknown                      // the rung did not report where it landed
	landedElsewhere                    // another page of the site
	landedLogin                        // a login page
	landedHome                         // the site's home page
)

// loginAddress matches a login page's host label or path segment.
var loginAddress = regexp.MustCompile(`(?i)(^|[/._-])(log-?in|sign-?in|sso|oauth2?|auth)([/._-]|$)`)

// localePrefix matches an address path's leading locale segment (/en, /en-us).
var localePrefix = regexp.MustCompile(`(?i)^/[a-z]{2}([-_][a-z]{2,4})?(/|$)`)

// landingPath is address's path as a page's identity: lower-cased, without a
// leading locale segment, a trailing slash or an index document.
func landingPath(address *url.URL) string {
	path := strings.ToLower(address.Path)
	if match := localePrefix.FindString(path); match != "" {
		path = "/" + strings.TrimPrefix(path, match)
	}
	for _, index := range []string{"index.html", "index.htm", "index.php"} {
		path = strings.TrimSuffix(path, "/"+index)
	}
	return strings.TrimRight(path, "/")
}

// slugOf reports whether longer is shorter with segments added (a slug after
// an id), shorter naming at least two segments: a bare section is a listing,
// not the page.
func slugOf(shorter, longer string) bool {
	return strings.Count(shorter, "/") >= 2 && strings.HasPrefix(longer, shorter+"/")
}

// classifyLanding is what final is to the page requested names.
func classifyLanding(ctx context.Context, requested, final string) landingKind {
	if final == "" {
		return landedUnknown
	}
	if samePage(ctx, requested, final) {
		return landedSame
	}
	want, wantErr := url.Parse(requested)
	got, gotErr := url.Parse(final)
	if wantErr != nil || gotErr != nil || got.Host == "" {
		return landedUnknown // samePage logged the parse failure
	}
	wantPath, gotPath := landingPath(want), landingPath(got)
	if wantPath == gotPath || slugOf(wantPath, gotPath) || slugOf(gotPath, wantPath) {
		return landedSame
	}
	switch {
	case loginAddress.MatchString(got.Hostname()+"/") || loginAddress.MatchString(got.Path):
		return landedLogin
	case gotPath == "":
		return landedHome
	}
	host := func(address *url.URL) string {
		return strings.TrimPrefix(strings.ToLower(address.Hostname()), "www.")
	}
	if host(want) != host(got) {
		// Another site's page is what the address points at — a shortener, a
		// resolver, a moved domain — not a substitute for it.
		obs.Logger(ctx).Info("harvest: the address redirected off its host",
			"target", logSource(requested), "landed", logSource(final))
		return landedSame
	}
	return landedElsewhere
}

// landed is gaps with the redirect a rung that asked for requested and was
// answered from final learned, replacing an earlier rung's: the page this rung
// stores is its own landing's. A rung that failed (err) learned nothing.
func (gaps carriedGaps) landed(ctx context.Context, requested, final string, err error) carriedGaps {
	return gaps.landedThrough(ctx, requested, final, nil, err)
}

// landedThrough is landed with the statuses of the redirect hops the rung
// followed (fetchPageHops): a same-host permanent move of the page (movedPage)
// is the page, noted as moved (carriedGaps.moved), never a different page.
func (gaps carriedGaps) landedThrough(ctx context.Context, requested, final string, hops []int, err error) carriedGaps {
	if err != nil {
		return gaps
	}
	gaps.redirect, gaps.refused, gaps.moved = "", "", ""
	where := "the site redirected " + safeURL(requested) + " to " + safeURL(final)
	landing := classifyLanding(ctx, requested, final)
	if landing == landedElsewhere && movedPage(requested, final, hops) {
		gaps.moved = safeURL(requested) + " moved permanently to " + safeURL(final)
		obs.Logger(ctx).Info("harvest: "+gaps.moved,
			"target", logSource(requested), "landed", logSource(final))
		landing = landedSame
	}
	switch landing {
	case landedUnknown:
		gaps.redirect = "the rung did not report the address it was answered from: " +
			"a redirect to another page could not be ruled out"
	case landedElsewhere:
		gaps.redirect = where + " — a different page (the requested page may no longer exist)"
	case landedLogin:
		gaps.refused = where + " — a login page: the requested page was not served"
	case landedHome:
		gaps.refused = where + " — the site's home page (the requested page may no longer exist)"
	case landedSame:
	}
	if gaps.redirect != "" || gaps.refused != "" {
		obs.Logger(ctx).Info("harvest: the rung was not answered from the requested page",
			"target", logSource(requested), "landed", logSource(final))
	}
	return gaps
}

// fail is a failure message naming the redirect that refused the last HTTP
// rung's page, when one did.
func (gaps carriedGaps) fail(message string) string {
	if gaps.refused == "" {
		return message
	}
	return gaps.refused + ". " + message
}

// readerLanding is the partial reason naming where a reader rung landed for
// source, read from the page's canonical address (rel=canonical, else og:url)
// in the reader's HTML of it doc: a reader follows redirects on its own side
// and reports only the address it was asked for. A page naming no canonical
// address leaves the redirect unknown, and that is named.
func readerLanding(ctx context.Context, source string, doc *html.Node) string {
	canonical := ""
	if targets := relTargets(doc, "canonical"); len(targets) > 0 {
		canonical = targets[0]
	} else if meta := firstWithAttr(doc, "property", "og:url", nil); meta != nil {
		canonical = strings.TrimSpace(nodeAttr(meta, "content"))
	}
	base, err := url.Parse(source)
	if canonical == "" || err != nil {
		return "the reader does not report the address it was answered from and the page names no canonical " +
			"address: a redirect to another page could not be ruled out"
	}
	if parsed, parseErr := url.Parse(canonical); parseErr == nil {
		canonical = base.ResolveReference(parsed).String()
	}
	where := "the reader was answered for " + safeURL(source) + " from " + safeURL(canonical)
	switch classifyLanding(ctx, source, canonical) {
	case landedElsewhere:
		return where + " — a different page (the requested page may no longer exist)"
	case landedLogin:
		return where + " — a login page: the requested page was not served"
	case landedHome:
		return where + " — the site's home page (the requested page may no longer exist)"
	case landedUnknown:
		return "the page's canonical address (" + logSource(canonical) + ") does not parse: " +
			"a redirect to another page could not be ruled out"
	case landedSame:
	}
	return ""
}

// fetchRung is an HTTP rung's request for target, gaps updated with the
// redirect it learned (landed): the page it stores is its own landing's.
func (h *Harvester) fetchRung(
	ctx context.Context,
	client *http.Client,
	target, ua string,
	headers map[string]string,
	gaps *carriedGaps,
) ([]byte, int, string, error) {
	response, hops, err := fetchPageHops(ctx, client, target, ua, headers, h.options.MaxBytes)
	*gaps = gaps.landedThrough(ctx, target, response.finalURL, hops, err)
	return response.body, response.status, response.contentType, err
}
