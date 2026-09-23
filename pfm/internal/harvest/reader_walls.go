package harvest

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"golang.org/x/net/html"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// A reader rung (jina, defuddle) returns the page as Markdown, and Markdown
// drops the markup a wall is read from (walls.go): schema.org's
// isAccessibleForFree flag, a paywall element, an app's sign-in gate. A
// paywalled article a reader served signed-out would be stored as the whole
// article. So a reader page passes the same wall check as an HTTP rung's
// page, on the reader's own HTML of it: Jina returns the page's rendered HTML
// when asked for it (readerHTMLFormat).

// readerHTMLFormat is the header asking Jina Reader for the page's HTML.
const readerHTMLFormat = "X-Return-Format"

// readerPageChecked is the convertedPage of a reader rung's markdown of source
// (readerPage), checked on the reader's HTML of the page as an HTTP rung's
// page is: the thread its markup states above what it carries (threadGap) and
// the recall gate on the markdown (measureContentRecall) always; the wall the
// page's markup shows where the markdown asks for a subscription or a sign-in
// (paywallCallPattern) or source is an app site that gates signed-out
// readers — prose alone never names a wall, and a page asking for neither has
// none to name. A check that could not run is named on the page
// (wallCheckFailed, readerChecksFailed), never stored as a clean page. A wall
// an earlier rung saw travels in the carried gaps (carriedGaps.wall) either way.
func (h *Harvester) readerPageChecked(ctx context.Context, source, markdown string) convertedPage {
	page := readerPage(source, markdown)
	wallAsked := paywallCallPattern.MatchString(markdown) || loginWalledHost(source)
	target := strings.TrimRight(h.options.JinaURL, "/") + "/" + source
	body, status, _, err := getBodyWithHeaders(ctx, h.jina, target, h.userAgent,
		map[string]string{readerHTMLFormat: "html"}, h.options.MaxBytes)
	why := "" // why the reader's HTML is no page to check, safe to repeat (errorReasonClass)
	switch {
	case err != nil:
		why = "the reader's request failed: " + errorReasonClass(err, "fetch error")
	case status >= 400:
		why = fmt.Sprintf("the reader answered HTTP %d", status)
	case jinaTargetError(body) != 0:
		why = fmt.Sprintf("the origin answered the reader HTTP %d", jinaTargetError(body))
	case isChallenge(body, status):
		why = "the reader was served a challenge"
	}
	var doc *html.Node
	if why == "" {
		if doc, err = html.Parse(bytes.NewReader(body)); err != nil {
			why = "unparseable HTML"
		}
	}
	if why != "" {
		logged := why
		if err != nil {
			logged = err.Error()
		}
		obs.Logger(ctx).Warn("harvest: the reader's HTML of the page could not be read; its checks did not run",
			"target", logSource(source), obs.FieldErr, logged)
		if wallAsked {
			page.wall = wallCheckFailed(why)
		}
		page.checks = readerChecksFailed(why)
		return page
	}
	if wallAsked {
		page.wall = pageWall(source, doc)
	}
	recall := ""
	if measure := measureContentRecall(visibleWords(doc), markdown); measure.low() {
		recall = "the reader's markdown kept " + measure.String()
	}
	page.checks = joinReasons(threadGap(ctx, doc), recall)
	return page
}

// readerChecksFailed is the partial reason of a reader page whose own HTML
// could not be checked, for the error class why.
func readerChecksFailed(why string) string {
	return "the reader's HTML of the page could not be read (" + why +
		"): its stated-count and recall checks did not run"
}

// wallCheckFailed is the partial reason of a reader page whose wall check was
// asked for and could not run, for the error class why.
func wallCheckFailed(why string) string {
	return "the page asks for a subscription or a sign-in and its wall check could not run (" + why +
		"): it may be only the preview the site serves signed-out"
}
