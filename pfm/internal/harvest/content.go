package harvest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/net/html"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

func (h *Harvester) convertFetchedContent(ctx context.Context, kind, source string, body []byte) (string, error) {
	converted, _, err := h.convertFetchedDocument(ctx, kind, source, body, nil)
	return converted, err
}

// convertedPage is what the converter learned about an HTML page beyond its
// markdown: the per-site extractor that rendered it ("" for the generic path),
// whether a browser render could close a gap it flagged, and siteAPI — the
// extractor reads its site's API (readsSiteAPI) and rendered the page from at
// least one API record, so a wall the site served in its place is not what is
// stored. unrendered is what an extractor that knew the page could not load
// (its API record refused, siteExtraction.unrendered): the fetch ladder names
// it in whatever a later rung stores (withGaps). nextPage is the page's own
// next page the pagination guard named (pagination.go) — a generic page's, or
// a reader's markdown's (readerPage) — carried the same way. listing names
// the unread later pages of an address naming its own page (pagedListing),
// and pager is whether the page showed a pager of that address, which answers
// whether a next page exists (pagination.go). Non-HTML kinds carry the zero
// value. stated is the extractor's reader of the stated count of what it could
// not load (siteExtraction.stated), carried with unrendered. wall is the wall
// the page's markup shows (pageWall) — a reader page's from the reader's HTML
// (readerPageChecked) — carried to whatever rung stores the page. checks is
// what a reader page's own HTML shows beyond its wall: the thread it states
// above what it carries and a markdown keeping little of its text
// (readerPageChecked), or why that HTML could not be checked.
type convertedPage struct {
	extractor         string
	renderMayComplete bool
	siteAPI           bool
	unrendered        string
	stated            func(content string) string
	wall              string
	checks            string
	landing           string // where a reader page was answered from, when not the page (readerLanding)
	nextPage          string
	listing           string
	pager             bool
}

// carriedGaps are what an earlier rung of this fetch learned the page lacks,
// from its HTML even when the rung refused that page (for its status or a
// wall): a site-API record's gap (api) and the page's un-followed next page
// (next) and the wall its markup showed (wall). Whatever rung stores the page
// names them (withGaps). pager is
// whether any rung saw the page's pager. redirect names the last rung's
// landing on another page, and refused its landing on a page that is no page
// at all — a login, the site's home — which no rung stores (landing.go).
type carriedGaps struct {
	api, next string
	pager     bool
	stated    func(content string) string
	wall      string
	redirect  string
	refused   string
}

// carry is gaps with this page's conversion's gaps filled in where no earlier
// rung saw one.
func (page convertedPage) carry(gaps carriedGaps) carriedGaps {
	if gaps.api == "" {
		gaps.api, gaps.stated = page.unrendered, page.stated
	}
	if gaps.next == "" {
		gaps.next = page.nextPage
	}
	gaps.pager = gaps.pager || page.pager
	if gaps.wall == "" {
		gaps.wall = page.wall
	}
	return gaps
}

// readerPage is the convertedPage of a reader rung's markdown of source: its
// own next page and whether it shows a pager, read from the markdown
// (markdownContinuation, pagerShown).
func readerPage(source, markdown string) convertedPage {
	page, err := url.Parse(source)
	if err != nil || page.Host == "" {
		return convertedPage{}
	}
	_, targets := markdownLinks(markdown, page)
	return convertedPage{
		nextPage: markdownContinuation(markdown, page),
		listing:  pagedListing(source),
		pager:    pagerShown(page, targets),
	}
}

// withGaps is content as a rung stores it, the fetch's carried gaps joined
// into its partial marker. A site-API record's gap travels with why the
// fetch's following failed (budget.note), so a page that shows only what the
// site renders is never stored as complete; one built from an API record
// (siteAPI) closed it. The next page travels unless the stored content
// provably followed it: a site extractor rendered it (an extractor that knows
// its site's pagination follows it, discourse.go); with no next page named,
// an address naming its own page whose pager no rung saw names its unread
// later pages (pagedListing). page is the conversion
// content came from (readerPage for a reader rung's markdown); a gap content
// already names is not repeated. A wall the page or an earlier rung showed
// is named unless an API record built the page (siteAPI).
func (page convertedPage) withGaps(content string, gaps carriedGaps, budget *loaderBudget) string {
	stored := partialReason(content)
	reason := stored
	if gaps.api != "" && !page.siteAPI {
		stated := "" // the count the stored content states of what was not loaded
		if gaps.stated != nil {
			stated = gaps.stated(partialBody(content))
		}
		if strings.Contains(reason, stated) {
			stated = ""
		}
		if !strings.Contains(reason, gaps.api) {
			note := budget.note()
			if strings.Contains(reason, note) {
				note = "" // the conversion content came from named it already
			}
			reason = joinReasons(reason, stated, gaps.api, note)
		} else {
			reason = joinReasons(reason, stated)
		}
	}
	// a wall the stored page or an earlier rung showed, and where the page was answered from (landing.go)
	for _, wall := range []string{page.wall, gaps.wall, page.checks, page.landing, gaps.redirect} {
		if wall != "" && !page.siteAPI && !strings.Contains(reason, wall) {
			reason = joinReasons(reason, wall)
		}
	}
	next := gaps.next
	if next == "" || (page.nextPage != "" && strings.Contains(reason, page.nextPage)) {
		next = page.nextPage // the stored page names its own next page
	}
	if next == "" && !gaps.pager && !page.pager {
		next = page.listing // no rung saw the paged address's pager
	}
	if next != "" && page.extractor == "" && !strings.Contains(reason, next) {
		reason = joinReasons(reason, next)
	}
	if reason == stored {
		return content
	}
	return withPartial(partialBody(content), reason)
}

// hashRouteShell reports whether source names a hash route (#/… or #!…) of a
// page whose raw HTML (raw, the last page an HTTP rung saw) is an empty app
// shell: only a browser running the app's router renders that route. A reader
// or an archive is sent the address less its fragment — a fragment never
// travels in a request — and would store the app's home view as the route. An
// anchor on a server-rendered page is not one: its HTML holds the page.
func hashRouteShell(ctx context.Context, source string, raw []byte) bool {
	parsed, err := url.Parse(source)
	if err != nil {
		obs.Logger(ctx).Warn("harvest: the address could not be parsed for its fragment", obs.FieldErr, err.Error())
		return false
	}
	if !strings.HasPrefix(parsed.Fragment, "/") && !strings.HasPrefix(parsed.Fragment, "!") {
		return false
	}
	markup := string(raw)
	if !strings.Contains(strings.ToLower(markup), "<script") {
		return false
	}
	text := htmlTagRe.ReplaceAllString(styleRe.ReplaceAllString(scriptRe.ReplaceAllString(markup, " "), " "), " ")
	return contentChars(text) < 100
}

// convertFetchedDocument is convertFetchedContent that also returns the
// convertedPage of an HTML page. budget is the web ladder's loader following
// for this fetch (loaders.go); nil converts the page as it is.
func (h *Harvester) convertFetchedDocument(
	ctx context.Context,
	kind, source string,
	body []byte,
	budget *loaderBudget,
) (string, convertedPage, error) {
	switch {
	case kind == kindTXT:
		return pageText(string(body)), convertedPage{}, nil
	case kind == kindCode:
		language := codeLanguage(source)
		if language == "" {
			language = languageXML // a code kind without a code extension is XML found by its prolog (textFormat)
		}
		return fenceCode(language, string(body)), convertedPage{}, nil
	case isCompressedKind(kind):
		inner, err := decompressDocument(kind, body, compressedDocumentCap)
		if err != nil {
			return "", convertedPage{}, fmt.Errorf("%s-compressed document could not be decompressed: %w", kind, err)
		}
		innerSource := innerDocumentName(kind, source)
		innerKind := classifyFetchedKind(innerSource, "", inner)
		if isCompressedKind(innerKind) {
			return "", convertedPage{}, fmt.Errorf("%s-compressed document is compressed again (%s)", kind, innerKind)
		}
		return h.convertFetchedDocument(ctx, innerKind, innerSource, inner, budget)
	}
	if h.options.Converter == nil {
		return "", convertedPage{}, errors.New("no injected converter configured for " + kind)
	}
	if kind != kindHTML {
		converted, err := h.options.Converter.Convert(ctx, kind, source, body)
		return pageText(converted), convertedPage{}, err
	}
	return h.convertHTML(ctx, source, body, budget)
}

// convertHTML is the HTML half of the converter boundary: a per-site tree
// extractor when one claims the page (site_extract.go), after the loaders of
// that site were followed into the page (loaders.go); otherwise the inert-
// container pre-pass (inert.go), the injected main-content converter, the
// recall gate over its output (recall.go) and the pagination guard
// (pagination.go). Every incompleteness it can see travels as the partial
// marker on the content — never only as a log line.
func (h *Harvester) convertHTML(
	ctx context.Context,
	source string,
	body []byte,
	budget *loaderBudget,
) (string, convertedPage, error) {
	// unparsed is the answer for a page that could not be parsed: its
	// recall is unmeasured, which a browser render may complete.
	unparsed := convertedPage{renderMayComplete: true}
	body = withoutConsentMarkup(body) // a consent dialog is never the page's content, on any rung
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		// x/net/html recovers from any malformed markup; an error here is a
		// reader failure. Convert the bytes as they are and flag the artifact
		// unmeasured rather than dropping the pre-pass silently.
		obs.Logger(ctx).Warn("harvest: HTML could not be parsed for the recall gate", obs.FieldErr, err.Error())
		converted, convertErr := h.options.Converter.Convert(ctx, kindHTML, source, body)
		if convertErr != nil {
			return "", convertedPage{}, convertErr
		}
		return withPartial(
			converted,
			"recall unmeasured: the page could not be parsed ("+errorReasonClass(err, "parse error")+")",
		), unparsed, nil
	}
	lazy := lazyLoadIncomplete(doc)
	rest := h.followForSite(ctx, source, doc, budget)
	// pager: the page shows its own address's pager (pagination.go).
	parsed, parseErr := url.Parse(source)
	pager := parseErr == nil && parsed.Host != "" && htmlPagerShown(doc, parsed)
	extraction, extractor, ok := extractForSite(source, doc)
	if ok {
		// A loader still in the page is a gap whatever the extractor counts;
		// the budget's note names why it was not loaded.
		reason := joinReasons(extraction.partial, rest.reason())
		if reason != "" || rest.left > 0 {
			reason = joinReasons(reason, budget.note())
		}
		if reason == "" && rest.left > 0 {
			reason = fmt.Sprintf("%d loader(s) left in the page, not followed", rest.left)
		}
		return withPartial(extraction.markdown, joinReasons(reason, lazy)), convertedPage{
			extractor: extractor,
			// A rate limit or the request cap ends the fetch's following; a
			// browser render pressing the same loaders would work around it.
			renderMayComplete: extraction.renderMayComplete && (budget == nil || !budget.policyStop),
			siteAPI:           extraction.apiRecord,
			pager:             pager,
		}, nil
	}
	// The generic path converts this page alone: a next page it links is
	// named, and a browser render — the same page — cannot close that gap, so
	// only the other reasons (lazy loading, low recall) escalate to one. Nor
	// can it load what an extractor that knew the page could not (its API
	// record refused), named with why the following failed.
	nextPage := ""
	if parseErr == nil {
		nextPage = paginationContinuation(doc, parsed)
	}
	// A wall the markup shows (walls.go) is named; a render of the same
	// signed-out page cannot close it either; nor can it load the thread the
	// page states or leaves behind a loader control (stated_gaps.go).
	wall := pageWall(source, doc)
	unclosed := joinReasons(wall, threadGap(ctx, doc), nextPage)
	if extraction.unrendered != "" {
		unclosed = joinReasons(extraction.unrendered, rest.reason(), budget.note(), unclosed)
	}
	done := func(content, reason string) (string, convertedPage, error) {
		return withPartial(content, joinReasons(reason, unclosed)), convertedPage{
			renderMayComplete: reason != "",
			unrendered:        extraction.unrendered,
			stated:            extraction.stated,
			wall:              wall,
			nextPage:          nextPage,
			listing:           pagedListing(source),
			pager:             pager,
		}, nil
	}
	input := body
	if unwrapInertContainers(ctx, doc) > 0 {
		var rendered bytes.Buffer
		if renderErr := html.Render(&rendered, doc); renderErr != nil {
			return "", convertedPage{}, fmt.Errorf("render the page with its inert containers surfaced: %w", renderErr)
		}
		input = rendered.Bytes()
	}
	converted, err := h.options.Converter.Convert(ctx, kindHTML, source, input)
	if err != nil {
		// What the extractor could not load is still named by the rung that stores the page.
		return "", convertedPage{unrendered: extraction.unrendered, stated: extraction.stated, wall: wall}, err
	}
	if budget.gateOnly(wall, converted) {
		obs.Logger(ctx).Info("harvest: the page holds nothing but a login wall", "target", logSource(source))
		return "", convertedPage{}, nil // never content; the fetch's failure names the wall
	}
	visible := visibleWords(doc)
	measure := measureContentRecall(visible, converted)
	if !measure.low() {
		return done(converted, lazy)
	}
	reason := "main-content extraction kept " + measure.String()
	fullDOM, ok := h.options.Converter.(FullDOMConverter)
	if !ok {
		return done(converted, joinReasons(reason+"; no full-DOM converter is wired", lazy))
	}
	full, fullErr := fullDOM.ConvertFullDOM(ctx, source, input)
	fullMeasure := measureContentRecall(visible, full)
	switch {
	case fullErr != nil:
		obs.Logger(ctx).Warn("harvest: full-DOM conversion failed", obs.FieldErr, fullErr.Error())
		reason += "; the full-DOM conversion failed: " + errorReasonClass(fullErr, "conversion error")
	case fullMeasure.low():
		reason += "; the full-DOM conversion kept only " + fullMeasure.String()
		if other := len(markdownWords(full)) - fullMeasure.extracted; other > 0 {
			reason += fmt.Sprintf(", and %d more of its words are not the page's visible text", other)
		}
	default:
		// The whole DOM, boilerplate included, is the complete artifact: it
		// holds the page's visible words, measured without its chrome.
		return done(full, lazy)
	}
	return done(converted, joinReasons(reason, lazy))
}

func classifyFetchedKind(source, contentType string, body []byte) string {
	kind := classifyKind(source, contentType, body)
	// Magic bytes route before the extension and the content type
	// (format_detect.go): an .xlsx named .xls is an xlsx, one compressed
	// document is its codec's kind (a compressed tar stays an archive), and
	// a text format converter.py dispatches keeps its own kind.
	found := detectFormat(source, body)
	switch found.class {
	case formatDocument:
		return found.kind
	case formatCompressed:
		if head, _ := decompressDocument(found.kind, body, 512); found.kind == kindXZ || !isTarHeader(head) {
			return found.kind
		}
		return kindTAR
	case formatText:
		if kind == kindHTML || kind == kindTXT || kind == kindJSON {
			return found.kind
		}
	case formatUnknown:
		// An archive or compression extension on bytes that carry no archive
		// signature (an empty file, text named .gz) is not an archive.
		if kind == kindTAR || kind == kindZIP || kind == kind7Z || kind == kindRAR {
			kind = kindHTML
		}
	}
	if kind != kindHTML && kind != kindTXT {
		return kind
	}
	// An empty body the server labels HTML (a WAF challenge: 202, no bytes) is
	// still a page, so a site extractor that reads its site's API may render it.
	if len(body) == 0 && strings.Contains(strings.ToLower(contentType), "html") {
		return kindHTML
	}
	if IsPlainText(source, contentType, string(body)) {
		return kindTXT
	}
	return kindHTML
}

func usableContent(content, kind string) bool {
	if content == "" {
		return false
	}
	if kind == kindHTML || kind == kindTXT {
		return len(strings.TrimSpace(content)) >= 1
	}
	return true
}

func isBibliographicLanding(content string) bool {
	if !hasTextHeading(content, "abstract") {
		return false
	}
	metadata := hasTextHeading(content, "fingerprint") || hasTextHeading(content, "cite this") ||
		strings.Contains(strings.ToLower(content), "research output")
	if !metadata {
		return false
	}
	for _, heading := range []string{"introduction", "background", "methods", "materials and methods", "methods and materials", "results", "discussion", "conclusion", "references"} {
		if hasTextHeading(content, heading) {
			return false
		}
	}
	return true
}

func hasTextHeading(content, want string) bool {
	want = strings.ToLower(strings.TrimSpace(want))
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimLeft(line, "#* ")
		line = strings.TrimSpace(strings.Trim(line, ":"))
		if strings.EqualFold(line, want) {
			return true
		}
	}
	return false
}

type bibliographicLandingHopKey struct{}

func bibliographicDocumentURL(body []byte, baseRaw string) string {
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return ""
	}
	var found string
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if found != "" {
			return
		}
		if node.Type == html.ElementNode && node.Data == "a" {
			href := strings.TrimSpace(nodeAttr(node, "href"))
			if href != "" {
				class := strings.ToLower(nodeAttr(node, "class"))
				label := strings.ToLower(strings.TrimSpace(nodeText(node)))
				parsed, parseErr := url.Parse(href)
				if parseErr == nil {
					path := strings.ToLower(parsed.Path)
					extension := filepath.Ext(path)
					isDocument := strings.Contains(class, "document-link") || strings.Contains(path, "/files/") ||
						strings.Contains(label, "full text") ||
						strings.Contains(label, "manuscript")
					supported := extension == extensionPDF || extension == ".doc" || extension == ".docx" ||
						extension == ".epub" ||
						extension == ".odt" ||
						extension == ".rtf" ||
						extension == extensionTXT
					if isDocument && (supported || strings.Contains(class, "document-link")) {
						base, baseErr := url.Parse(baseRaw)
						if baseErr == nil {
							resolved := base.ResolveReference(parsed)
							resolved.Fragment = ""
							if (resolved.Scheme == schemeHTTP || resolved.Scheme == schemeHTTPS) &&
								validateFetchURL(resolved.String(), false) == nil {
								found = resolved.String()
							}
						}
					}
				}
			}
		}
		for child := node.FirstChild; child != nil && found == ""; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return found
}

func contentChars(content string) int { return len([]rune(strings.TrimSpace(content))) }

// blankRenderPage reports whether a rendered document carries no visible
// text. Chrome serialises at least <html><head></head><body></body></html>
// for any navigated page — the raw string is never empty, so emptiness is
// judged on tag-stripped text: script/style bodies dropped, tags removed,
// and effectively nothing left. BLANK is not THIN: a short-but-real page
// must fall through to the 500-char acceptance floor instead, where it
// reads as "ran and could not pass", never as an empty render.
var (
	scriptRe  = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	styleRe   = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	htmlTagRe = regexp.MustCompile(`(?s)<[^>]*>`)
)

func blankRenderPage(markup string) bool {
	text := scriptRe.ReplaceAllString(markup, " ")
	text = styleRe.ReplaceAllString(text, " ")
	text = htmlTagRe.ReplaceAllString(text, " ")
	return contentChars(text) < 10
}

func kindFromName(source string) string {
	return DetectKind(source)
}
