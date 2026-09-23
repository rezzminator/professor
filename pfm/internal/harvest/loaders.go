package harvest

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// Loader following. A discussion page served from the server holds the first
// part of its tree; the rest sits behind loaders ("3 more replies", "View more
// comments") that fetch a fragment of the tree, and behind links to a
// separate page for a reply chain past the page's depth ("Continue this
// thread"). A registered site extractor names its loaders (siteExtractor.
// loaders); followLoaders requests each one from Go and splices the answer into
// the page in the loader's place, until no loader is left, so the extractor
// then renders the whole tree in thread order. It is deterministic (DOM order,
// one request at a time, in one cookie session like a reader's), polite
// (loaderPace between requests; a 429 or a bot wall ends the following and is
// never retried) and bounded (loaderRequestCap per fetch).
// Whatever is still unfollowed stays in the page, where the extractor counts
// it as a gap, and the budget's note names why. A later conversion of the same
// fetch (the next rung's page, the browser's render) gets every answer already
// followed replayed into its page from the budget, never requested again and
// never silently lost: an answer the store's bound left out, or one that will
// not graft into the later page, is named on that page as a gap. An answer
// that came from off the site through a redirect is refused like a loader
// pointing there.

const (
	// loaderRequestCap bounds the loader requests one fetch spends, across
	// every render of the page the ladder converts.
	loaderRequestCap = 200
	// loaderPace is the pause before each loader request: a reader's pace of
	// pressing, not a crawler's — a faster one meets the site's bot wall.
	loaderPace = time.Second
	// loaderFailureStop ends the following after this many failed requests
	// in a row: the site is refusing them, and more would only repeat it.
	loaderFailureStop = 3
	// graftErrorReasonMaxLen bounds graftErrorClass's answer: today's only
	// graft (reddit.go) wraps a page title read off the wire, naturally
	// short, but a future extractor's graft could wrap something longer —
	// this caps what any graft failure may repeat into a partial artifact.
	graftErrorReasonMaxLen = 200
	// loaderReplayCap bounds the bytes of followed answers one fetch keeps
	// for replay into a later conversion: loaderRequestCap answers of up to
	// Options.MaxBytes each would otherwise stay resident together.
	loaderReplayCap = 32 << 20
	// loaderBackoffCap is the longest back-off a site may ask of the next
	// request (an API's backoff field) that the following waits out; a
	// longer one ends the following, the site's request honoured by never
	// asking again in this fetch.
	loaderBackoffCap = 30 * time.Second
)

// graftErrorClass is the ONE named exception to errorReasonClass
// (recall.go): a loader's graft failure already names page content read off
// the wire — a title, a content type — never a local path or a worker's
// stderr, so it is safe to repeat rather than classify away. Bounded to
// graftErrorReasonMaxLen so a future extractor's graft can never leak more
// than that into Partial, the PARTIAL receipt or the cache.
func graftErrorClass(err error) string {
	text := err.Error()
	if len(text) > graftErrorReasonMaxLen {
		return text[:graftErrorReasonMaxLen] + "…"
	}
	return text
}

// pageLoader is one loader in a page: the request that answers it and how its
// answer is spliced in.
type pageLoader struct {
	// key identifies the loader: the same loader met twice (a page carrying a
	// copy of its tree) is requested once.
	key string
	// label names the loader to a reader: "\"3 more replies\" loader".
	label   string
	method  string
	target  string // absolute URL
	form    url.Values
	headers map[string]string
	// graft splices the answer into the page in the loader's place; an error
	// leaves the loader where it was, a gap.
	graft func(body []byte, contentType string) error
	// drop removes a copy of a loader whose answer is already in the page.
	drop func()
	// rateLimited, when set, reads an error answer as the site's rate limit
	// (a site that refuses with 403 and says so in its body, not with 429);
	// such an answer ends the following like a 429.
	rateLimited func(status int, body []byte) bool
	// backoff, when set, reads from a followed answer how long the site asks
	// the next request to wait; a wait longer than the pace replaces it.
	backoff func(body []byte) time.Duration
}

// loaderBudget is one fetch's loader following, shared by every conversion of
// the page (the HTTP rung's and the browser rung's), so the cap is per fetch
// and a request that failed once is not sent again.
type loaderBudget struct {
	limit       int
	pace        time.Duration
	requests    int
	followed    map[string]bool
	attempted   map[string]bool
	failures    []string
	consecutive int
	// hold is the back-off the last followed answer asked of the next
	// request; 0 when it asked none.
	hold time.Duration
	// failed holds the loaders whose request failed or was refused.
	failed map[string]bool
	// jar carries the cookies the site sets across the fetch's requests.
	jar http.CookieJar
	// stopped is why following ended before every loader was requested; ""
	// while it may continue.
	stopped string
	// policyStop marks a stop no other rung may work around: the site rate
	// limited us, or the cap was reached.
	policyStop bool
	// answers holds each followed loader's answer, replayed into a later
	// conversion's page, up to replayLimit bytes in all (replayBytes kept).
	answers     map[string]loaderAnswer
	replayLimit int
	replayBytes int
}

// loaderAnswer is one followed loader's answer as the site sent it.
type loaderAnswer struct {
	body        []byte
	contentType string
}

func newLoaderBudget(ctx context.Context) *loaderBudget {
	budget := &loaderBudget{
		limit:       loaderRequestCap,
		pace:        loaderPace,
		followed:    map[string]bool{},
		attempted:   map[string]bool{},
		failed:      map[string]bool{},
		answers:     map[string]loaderAnswer{},
		replayLimit: loaderReplayCap,
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		obs.Logger(ctx).Warn("harvest: no cookie jar for loader following; its requests go without cookies",
			obs.FieldErr, err.Error())
		return budget
	}
	budget.jar = jar
	return budget
}

// note names what the following could not do, for the partial marker of a
// page whose gaps remain; "" when nothing went wrong.
func (budget *loaderBudget) note() string {
	if budget == nil {
		return ""
	}
	var parts []string
	if len(budget.failures) > 0 {
		shown := budget.failures
		if len(shown) > 3 {
			shown = shown[:3]
		}
		parts = append(parts, fmt.Sprintf("%d loader request(s) failed (%s)", len(budget.failures),
			strings.Join(shown, "; ")))
	}
	if budget.stopped != "" {
		parts = append(parts, "loader following stopped: "+budget.stopped)
	}
	return strings.Join(parts, "; ")
}

// loaderRemainder is what following left in one conversion's page: left, the
// loaders still named in it — each a gap, whatever the extractor counts — and
// unreplayed, one reason per answer this fetch followed that the page could
// not take, named apart from a loader never followed.
type loaderRemainder struct {
	left       int
	unreplayed []string
}

// reason names the unreplayed answers for the partial marker; "" when none.
func (rest loaderRemainder) reason() string {
	return strings.Join(rest.unreplayed, "; ")
}

// followLoaders requests every loader extractor names in doc and splices each
// answer in, then reports what is left in doc (loaderRemainder).
func (h *Harvester) followLoaders(
	ctx context.Context,
	doc *html.Node,
	page *url.URL,
	extractor siteExtractor,
	budget *loaderBudget,
) loaderRemainder {
	unreplayed := map[string]string{}
	h.followRounds(ctx, doc, page, extractor, budget, unreplayed)
	var rest loaderRemainder
	named := map[string]bool{}
	for _, loader := range extractor.loaders(doc, page) {
		if named[loader.key] {
			continue
		}
		named[loader.key] = true
		rest.left++
		if why, ok := unreplayed[loader.key]; ok {
			rest.unreplayed = append(rest.unreplayed, loader.label+" was followed, but "+why)
		}
	}
	return rest
}

// followRounds re-reads the page after each round because an answer can hold
// loaders of its own. A loader this fetch followed already is answered from
// the budget: dropped when its answer is in doc (a copy of it), replayed into
// doc otherwise; unreplayed collects why an answer could not be. It returns
// when no loader is left to request, or when the budget stops it.
func (h *Harvester) followRounds(
	ctx context.Context,
	doc *html.Node,
	page *url.URL,
	extractor siteExtractor,
	budget *loaderBudget,
	unreplayed map[string]string,
) {
	// grafted: the loaders whose answer is in doc.
	grafted := map[string]bool{}
	for budget.stopped == "" {
		progressed := false
		for _, loader := range extractor.loaders(doc, page) {
			if _, done := unreplayed[loader.key]; done {
				continue
			}
			switch {
			case grafted[loader.key]:
				loader.drop()
			case budget.followed[loader.key]:
				answer, kept := budget.answers[loader.key]
				if !kept {
					unreplayed[loader.key] = fmt.Sprintf(
						"its answer was not kept for replay (the fetch's replay store is bounded at %d bytes)",
						budget.replayLimit)
					continue
				}
				if err := loader.graft(answer.body, answer.contentType); err != nil {
					obs.Logger(ctx).Warn("harvest: a followed loader's answer did not graft into this page; "+
						"it stays a gap", "kind", loader.label, "target", safeURL(loader.target), obs.FieldErr, err.Error())
					unreplayed[loader.key] = "its answer would not graft into this page: " + graftErrorClass(err)
					continue
				}
				grafted[loader.key] = true
			case budget.attempted[loader.key]:
				continue
			default:
				if !h.followLoader(ctx, loader, page, extractor, budget) {
					return
				}
				grafted[loader.key] = budget.followed[loader.key]
			}
			progressed = true
		}
		if !progressed {
			return
		}
	}
}

// followLoader sends one loader's request and grafts its answer. false means
// the budget stopped the following.
func (h *Harvester) followLoader(
	ctx context.Context,
	loader pageLoader,
	page *url.URL,
	extractor siteExtractor,
	budget *loaderBudget,
) bool {
	budget.attempted[loader.key] = true
	target, err := url.Parse(loader.target)
	if err != nil || !extractor.mayRequest(page, target) {
		// A loader pointing off the site is never requested: the page does
		// not get to choose where the harvester sends a request.
		obs.Logger(ctx).Warn("harvest: a loader pointing off the site was not followed",
			"kind", loader.label, "target", safeURL(loader.target))
		budget.failures = append(budget.failures, loader.label+" points off the site: "+safeURL(loader.target))
		budget.failed[loader.key] = true
		return true
	}
	if budget.requests >= budget.limit {
		budget.stopped = fmt.Sprintf("the cap of %d loader requests per page was reached", budget.limit)
		budget.policyStop = true
		return false
	}
	wait := max(budget.pace, budget.hold)
	if budget.hold > loaderBackoffCap {
		budget.stopped = fmt.Sprintf("the site asked for a %s back-off before its next request, past the %s "+
			"a fetch waits; not requested", budget.hold, loaderBackoffCap)
		budget.policyStop = true
		return false
	}
	budget.hold = 0
	if err := h.nowClock().Sleep(ctx, wait); err != nil {
		obs.Logger(ctx).Debug("harvest: loader following stopped: fetch cancelled",
			"kind", loader.label, obs.FieldErr, err.Error())
		budget.stopped = "the fetch was cancelled: " + errorReasonClass(err, "cancelled")
		return false
	}
	budget.requests++
	request := gatewayRequest{
		url:     loader.target,
		client:  h.client,
		ua:      h.userAgent,
		headers: make(http.Header, len(loader.headers)),
		max:     h.options.MaxBytes,
		jar:     budget.jar,
		method:  loader.method,
	}
	for key, value := range loader.headers {
		request.headers.Set(key, value)
	}
	if loader.form != nil {
		request.body = []byte(loader.form.Encode())
	}
	response, err := gatewayAttempt(ctx, request)
	if err != nil {
		return budget.fail(ctx, loader, err.Error(), errorReasonClass(err, "request failed"))
	}
	if final, parseErr := url.Parse(response.finalURL); parseErr != nil || !extractor.mayRequest(page, final) {
		// A redirect took the request off the site: its answer is not the
		// site's, whatever it holds.
		offSite := "answered from off the site (" + safeURL(response.finalURL) + ")"
		return budget.fail(ctx, loader, offSite, offSite)
	}
	switch {
	case response.status == http.StatusTooManyRequests:
		budget.stopped = fmt.Sprintf("the site answered HTTP 429 (rate limited) after %d request(s); not retried",
			budget.requests)
		budget.policyStop = true
		return false
	case response.status >= 400 && loader.rateLimited != nil && loader.rateLimited(response.status, response.body):
		budget.stopped = fmt.Sprintf(
			"the site answered HTTP %d (rate limit exhausted) after %d request(s); not retried",
			response.status,
			budget.requests,
		)
		budget.policyStop = true
		return false
	case response.status >= 400:
		status := fmt.Sprintf("HTTP %d", response.status)
		return budget.fail(ctx, loader, status, status)
	}
	if err := loader.graft(response.body, response.contentType); err != nil {
		// The graft's own error names what the site's page held (its title,
		// its content type) — content already public on the wire, never a
		// local path or a worker's stderr, so it passes through unclassified.
		if isChallenge(response.body, response.status) {
			// Judged only on an answer that held nothing to graft: a comment
			// can quote a wall's phrase.
			budget.fail(ctx, loader, err.Error(), graftErrorClass(err))
			budget.stopped = fmt.Sprintf("the site answered a bot wall after %d request(s); not retried",
				budget.requests)
			return false
		}
		return budget.fail(ctx, loader, err.Error(), graftErrorClass(err))
	}
	budget.followed[loader.key] = true
	budget.consecutive = 0
	if loader.backoff != nil {
		budget.hold = loader.backoff(response.body)
	}
	if budget.replayBytes+len(response.body) > budget.replayLimit {
		obs.Logger(ctx).Warn("harvest: a followed loader's answer is past the replay bound; a later conversion "+
			"names it a gap", "kind", loader.label, "target", safeURL(loader.target),
			"bytes", len(response.body), "kept", budget.replayBytes, "limit", budget.replayLimit)
		return true
	}
	budget.replayBytes += len(response.body)
	budget.answers[loader.key] = loaderAnswer{body: response.body, contentType: response.contentType}
	return true
}

// fail records one failed loader request; false once loaderFailureStop
// requests in a row have failed. detail is the full diagnostic that reaches
// the log (obs); class is the short, sanitized reason a partial artifact and
// the public result may repeat — errorReasonClass's output for an error the
// caller does not already know to be safe, detail itself otherwise.
func (budget *loaderBudget) fail(ctx context.Context, loader pageLoader, detail, class string) bool {
	obs.Logger(ctx).Warn("harvest: loader request failed",
		"kind", loader.label, "target", safeURL(loader.target), obs.FieldErr, detail)
	budget.failed[loader.key] = true
	budget.failures = append(budget.failures, fmt.Sprintf("%s (%s): %s", loader.label, safeURL(loader.target), class))
	budget.consecutive++
	if ctx.Err() != nil {
		budget.stopped = "the fetch was cancelled: " + errorReasonClass(ctx.Err(), "cancelled")
		return false
	}
	if budget.consecutive >= loaderFailureStop {
		budget.stopped = fmt.Sprintf("%d loader requests in a row failed", budget.consecutive)
		return false
	}
	return true
}

// spliceInPlace puts nodes where at is, in order, and removes at. Each node is
// detached from wherever it was first.
func spliceInPlace(at *html.Node, nodes []*html.Node) {
	parent := at.Parent
	if parent == nil {
		return
	}
	for _, node := range nodes {
		if node.Parent != nil {
			node.Parent.RemoveChild(node)
		}
		parent.InsertBefore(node, at)
	}
	parent.RemoveChild(at)
}

// detach removes node from its parent, if it has one.
func detach(node *html.Node) {
	if node.Parent != nil {
		node.Parent.RemoveChild(node)
	}
}
