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
// it as a gap, and the budget's note names why.

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
)

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
	// jar carries the cookies the site sets across the fetch's requests.
	jar http.CookieJar
	// stopped is why following ended before every loader was requested; ""
	// while it may continue.
	stopped string
	// policyStop marks a stop no other rung may work around: the site rate
	// limited us, or the cap was reached.
	policyStop bool
}

func newLoaderBudget(ctx context.Context) *loaderBudget {
	budget := &loaderBudget{
		limit:     loaderRequestCap,
		pace:      loaderPace,
		followed:  map[string]bool{},
		attempted: map[string]bool{},
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

// followLoaders requests every loader extractor names in doc and splices each
// answer in, re-reading the page after each round because an answer can hold
// loaders of its own. It returns when no loader is left to request, or when
// the budget stops it.
func (h *Harvester) followLoaders(
	ctx context.Context,
	doc *html.Node,
	page *url.URL,
	extractor siteExtractor,
	budget *loaderBudget,
) {
	for budget.stopped == "" {
		progressed := false
		for _, loader := range extractor.loaders(doc, page) {
			switch {
			case budget.followed[loader.key]:
				loader.drop()
				progressed = true
				continue
			case budget.attempted[loader.key]:
				continue
			}
			if !h.followLoader(ctx, loader, extractor, budget) {
				return
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
	extractor siteExtractor,
	budget *loaderBudget,
) bool {
	budget.attempted[loader.key] = true
	target, err := url.Parse(loader.target)
	if err != nil || !extractor.ownsHost(strings.ToLower(target.Hostname())) {
		// A loader pointing off the site is never requested: the page does
		// not get to choose where the harvester sends a request.
		obs.Logger(ctx).Warn("harvest: a loader pointing off the site was not followed",
			"kind", loader.label, "target", safeURL(loader.target))
		budget.failures = append(budget.failures, loader.label+" points off the site: "+safeURL(loader.target))
		return true
	}
	if budget.requests >= budget.limit {
		budget.stopped = fmt.Sprintf("the cap of %d loader requests per page was reached", budget.limit)
		budget.policyStop = true
		return false
	}
	if err := h.nowClock().Sleep(ctx, budget.pace); err != nil {
		budget.stopped = "the fetch was cancelled: " + err.Error()
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
	switch {
	case err != nil:
		return budget.fail(ctx, loader, err.Error())
	case response.status == http.StatusTooManyRequests:
		budget.stopped = fmt.Sprintf("the site answered HTTP 429 (rate limited) after %d request(s); not retried",
			budget.requests)
		budget.policyStop = true
		return false
	case response.status >= 400:
		return budget.fail(ctx, loader, fmt.Sprintf("HTTP %d", response.status))
	}
	if err := loader.graft(response.body, response.contentType); err != nil {
		if isChallenge(response.body, response.status) {
			// Judged only on an answer that held nothing to graft: a comment
			// can quote a wall's phrase.
			budget.fail(ctx, loader, err.Error())
			budget.stopped = fmt.Sprintf("the site answered a bot wall after %d request(s); not retried",
				budget.requests)
			return false
		}
		return budget.fail(ctx, loader, err.Error())
	}
	budget.followed[loader.key] = true
	budget.consecutive = 0
	return true
}

// fail records one failed loader request; false once loaderFailureStop
// requests in a row have failed.
func (budget *loaderBudget) fail(ctx context.Context, loader pageLoader, cause string) bool {
	obs.Logger(ctx).Warn("harvest: loader request failed",
		"kind", loader.label, "target", safeURL(loader.target), obs.FieldErr, cause)
	budget.failures = append(budget.failures, fmt.Sprintf("%s (%s): %s", loader.label, safeURL(loader.target), cause))
	budget.consecutive++
	if ctx.Err() != nil {
		budget.stopped = "the fetch was cancelled: " + ctx.Err().Error()
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
