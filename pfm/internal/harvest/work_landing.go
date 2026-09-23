package harvest

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// A work read by identifier with caller headers (readWork, ForWork): the
// identifier resolves as without headers — doi.org, the resolvers and the
// metadata APIs never receive them — and once the landing URL is known the
// headers go to that URL's origin only: the landing page read and a full-text
// file on the same origin. Mirrors, open-access copies and repositories on
// another origin, Wayback and reader services never receive them, and an
// answer served from one names the omission in partial.

// headerlessNote is the words partial carries for a read the caller's headers
// did not reach.
const headerlessNote = "without the caller's headers"

// landingRead is one work read scoped to its landing origin: the candidate
// URL the known-ID ladder's answer came from.
type landingRead struct {
	mu     sync.Mutex
	served string
}

// pendingLanding is the caller scope of a work read whose landing origin is
// not known yet.
func pendingLanding(ctx context.Context) (callerScope, bool) {
	scope, ok := ctx.Value(callerScopeKey{}).(callerScope)
	return scope, ok && scope.headers.Len() > 0 && scope.origin == "" && scope.landing == nil
}

// throughLanding reports a work read readThroughLanding scoped: its alias
// cache is skipped, since a cached answer cannot say which origin served it.
func throughLanding(ctx context.Context) bool {
	scope, ok := ctx.Value(callerScopeKey{}).(callerScope)
	return ok && scope.landing != nil
}

// noteServed records the URL a known-ID candidate's answer came from.
func noteServed(ctx context.Context, raw string) {
	if scope, ok := ctx.Value(callerScopeKey{}).(callerScope); ok && scope.landing != nil {
		scope.landing.mu.Lock()
		scope.landing.served = raw
		scope.landing.mu.Unlock()
	}
}

// readThroughLanding reads a work whose caller headers wait for its landing
// origin; false when ctx carries no such read. The landing page is read first
// with the headers; a clean page (no error, nothing partial) that carries the
// full text (landingFullText) is the answer. Otherwise the known-ID ladder
// runs as without headers — a candidate on the landing origin gets them — and
// its answer names, in partial, a landing page passed over for carrying no
// full text and, when another origin served it, the omission. A walled or
// abstract-only landing page is the answer only when the ladder found nothing.
func (h *Harvester) readThroughLanding(
	ctx context.Context,
	source string,
	kind IdentifierKind,
	options FetchOptions,
) (Result, bool) {
	scope, pending := pendingLanding(ctx)
	if !pending {
		return Result{}, false
	}
	landing, why := "", "the identifier names no landing URL the harvester resolves"
	if kind == IdentifierDOI {
		landing, why = h.doiLanding(ctx, DOIFrom(source))
	}
	origin := webOrigin(landing)
	state := &landingRead{}
	scoped := context.WithValue(
		ctx,
		callerScopeKey{},
		callerScope{headers: scope.headers, origin: origin, landing: state},
	)
	var page Result
	abstractOnly := ""
	if origin != "" {
		page = h.fetchURLWithPolicy(scoped, landing, options, false)
		page.Source = source
		if page.Error == "" && page.Partial == "" {
			if slices.ContainsFunc(page.Rungs, func(rung string) bool { return strings.HasPrefix(rung, "oa:") }) {
				page.Partial = "read through the landing page's full-text link; the caller's headers went only to " + origin
				return page, true
			}
			full, measure := landingFullText(page.Content)
			if full {
				return page, true
			}
			abstractOnly = "the landing page at " + origin + " carries no full text (" + measure + ")"
		}
	}
	result := h.fetchKnownID(scoped, source, kind, options)
	if result.Error != "" {
		if origin != "" && page.Error == "" {
			if abstractOnly != "" {
				page.Partial = abstractOnly + ", and no open-access full text was found"
			}
			return page, true
		}
		return result, true
	}
	state.mu.Lock()
	served := state.served
	state.mu.Unlock()
	reason := "read from a copy off the landing origin " + origin + ", " + headerlessNote
	switch {
	case origin != "" && webOrigin(served) == origin:
		reason = ""
	case origin == "":
		reason = "read " + headerlessNote + ": " + why
	}
	for _, prior := range []string{result.Partial, abstractOnly} {
		if prior != "" && reason != "" {
			reason = prior + "; " + reason
		} else if prior != "" {
			reason = prior
		}
	}
	result.Partial = reason
	return result, true
}

// doiLanding asks doi.org — a resolver, so without the caller's headers and
// without following the redirect — for the DOI's landing URL. On no answer it
// returns "" and why, an outage named apart from a DOI with no landing.
func (h *Harvester) doiLanding(ctx context.Context, doi string) (landing, why string) {
	if doi == "" {
		return "", "the DOI does not parse"
	}
	client := *h.resolver().client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := gatewayDo(ctx, gatewayRequest{
		url: "https://doi.org/" + doi, client: &client, ua: h.resolver().scholarlyUA(), max: 64 << 10,
	})
	if err != nil {
		obs.Logger(ctx).Info("harvest: the DOI landing lookup failed", "doi", doi, "err", err)
		return "", "the landing lookup at doi.org failed"
	}
	if closeErr := response.Body.Close(); closeErr != nil {
		obs.Logger(ctx).Info("harvest: the DOI landing answer did not close", "doi", doi, "err", closeErr)
	}
	location, err := response.Location()
	if err != nil || webOrigin(location.String()) == "" || ClassifyIdentifier(location.String()) != IdentifierNone {
		return "", fmt.Sprintf("doi.org answered %d with no landing URL", response.StatusCode)
	}
	return location.String(), ""
}
