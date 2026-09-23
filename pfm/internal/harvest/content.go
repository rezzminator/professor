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
// it in whatever a later rung stores (withAPIGap). Non-HTML kinds carry the
// zero value.
type convertedPage struct {
	extractor         string
	renderMayComplete bool
	siteAPI           bool
	unrendered        string
}

// firstAPIGap is gap, the API gap an earlier rung of this fetch saw, or else
// the one this page's conversion saw.
func (page convertedPage) firstAPIGap(gap string) string {
	if gap != "" {
		return gap
	}
	return page.unrendered
}

// withAPIGap is content as a rung stores it after a site-API extractor's
// record failed to load (gap, from firstAPIGap): the gap and why the fetch's
// following failed (budget.note) joined into its partial marker, so a page
// that shows only what the site renders is never stored as complete. page is
// the conversion content came from (the zero value for a reader rung's
// markdown): one built from an API record (siteAPI) closed the gap, and
// content that already names it is kept as it is.
func (page convertedPage) withAPIGap(content, gap string, budget *loaderBudget) string {
	reason := partialReason(content)
	if gap == "" || page.siteAPI || strings.Contains(reason, gap) {
		return content
	}
	note := budget.note()
	if strings.Contains(reason, note) {
		note = "" // the conversion content came from named it already
	}
	return withPartial(partialBody(content), joinReasons(reason, gap, note))
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
	if kind == kindTXT {
		return pageText(string(body)), convertedPage{}, nil
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
		}, nil
	}
	// The generic path converts this page alone: a next page it links is
	// named, and a browser render — the same page — cannot close that gap, so
	// only the other reasons (lazy loading, low recall) escalate to one. Nor
	// can it load what an extractor that knew the page could not (its API
	// record refused), named with why the following failed.
	unclosed := ""
	if parsed, parseErr := url.Parse(source); parseErr == nil {
		unclosed = paginationContinuation(doc, parsed)
	}
	if extraction.unrendered != "" {
		unclosed = joinReasons(extraction.unrendered, rest.reason(), budget.note(), unclosed)
	}
	done := func(content, reason string) (string, convertedPage, error) {
		return withPartial(content, joinReasons(reason, unclosed)), convertedPage{
			renderMayComplete: reason != "",
			unrendered:        extraction.unrendered,
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
		return "", convertedPage{}, err
	}
	visible := visibleWords(doc)
	measure := measureRecall(len(visible), converted)
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
	if kind != kindHTML && kind != kindTXT {
		return kind
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
