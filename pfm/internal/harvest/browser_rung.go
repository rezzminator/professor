package harvest

import (
	"context"
	"net/url"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// browserCandidate is a usable browser render, converted, and what the HTTP
// rungs left for it to beat.
type browserCandidate struct {
	source    string // the page requested
	finalURL  string // the address of the document the render holds; "" when unknown
	converted string
	extractor string // the per-site extractor that claimed the render; "" for the generic path
	// earlierChars is the content length the HTTP rungs last converted, and
	// appShell reports an app shell they detected (see appshell.go).
	earlierChars int
	appShell     bool
	// kept reports a flagged HTTP page kept for the browser to beat
	// (partialPage), and keptExtractor the extractor that claimed it.
	kept          bool
	keptExtractor string
}

// browserRenderWins reports whether the ladder stores the browser render.
//
// The thin-page floor holds as on the HTTP rungs: a JS paywall overlay
// converting to a few hundred chars is a shell, not the article, unless an
// extractor claimed the render. With no flagged page kept, the render must be
// longer than what the earlier rungs converted, follow an app shell, or be
// claimed by an extractor: a wall's text is not the site's content.
//
// Against a kept flagged page the render must first BE the requested page: its
// document at the source's address (samePage), and — when an extractor claimed
// the kept page — claimed by that extractor too. A consent, login or age-gate
// interstitial, or a load-time redirect, is otherwise an unflagged page of any
// length that would replace the real one. Being the page, a render with no
// partial marker wins even when shorter: the flagged page's length includes
// its gap list.
func browserRenderWins(ctx context.Context, candidate browserCandidate) bool {
	chars := contentChars(partialBody(candidate.converted))
	claimed := candidate.extractor != ""
	if !claimed && chars < 500 {
		return false
	}
	if landing := classifyLanding(ctx, candidate.source, candidate.finalURL); landing == landedLogin ||
		landing == landedHome {
		obs.Logger(ctx).Info("harvest: the browser landed on a login or the home page; it is no page to store",
			"target", logSource(candidate.source), "landed", logSource(candidate.finalURL))
		return false
	}
	if !candidate.kept {
		return chars > candidate.earlierChars || candidate.appShell || claimed
	}
	if !samePage(ctx, candidate.source, candidate.finalURL) {
		landed := logSource(candidate.finalURL)
		if landed == "" {
			landed = "an address the browser did not report"
		}
		obs.Logger(ctx).Info("harvest: the browser render is not the requested page; the flagged page is kept",
			"target", logSource(candidate.source), "landed", landed)
		return false
	}
	if candidate.keptExtractor != "" && candidate.extractor != candidate.keptExtractor {
		obs.Logger(ctx).Info("harvest: the browser render is not a page its extractor claims; the flagged page is kept",
			"target", logSource(candidate.source), "kind", candidate.keptExtractor)
		return false
	}
	return chars > candidate.earlierChars || candidate.appShell || partialReason(candidate.converted) == ""
}

// samePage reports whether final addresses the page source names: the same
// host (case and a leading "www." aside) and path (a trailing slash aside),
// whatever the scheme, query or fragment — a redirect to https or to the
// canonical host is the same page, a redirect to a login, consent or age-gate
// path is not. An empty or unparsable final never proves it.
func samePage(ctx context.Context, source, final string) bool {
	if final == "" {
		return false
	}
	want, err := url.Parse(source)
	if err != nil {
		obs.Logger(ctx).Warn("harvest: the requested page does not parse",
			"target", logSource(source), obs.FieldErr, err.Error())
		return false
	}
	got, err := url.Parse(final)
	if err != nil {
		obs.Logger(ctx).Warn("harvest: the browser's final address does not parse",
			"target", logSource(source), "landed", logSource(final), obs.FieldErr, err.Error())
		return false
	}
	host := func(address *url.URL) string {
		return strings.TrimPrefix(strings.ToLower(address.Hostname()), "www.")
	}
	path := func(address *url.URL) string { return strings.TrimSuffix(address.Path, "/") }
	return host(want) == host(got) && want.Port() == got.Port() && path(want) == path(got)
}
