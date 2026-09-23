package harvest

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// The recall gate. A main-content extractor keeps the article and discards
// what it classifies as boilerplate — and on a page whose content it does not
// recognise (a comment tree of custom elements, a card grid) that is most of
// the page. Nothing in the extractor says so: it reports success, and the
// cache stores the truncated artifact as the page. The gate measures it:
// extracted words against the words a reader sees in the DOM. Below the floor
// the page is converted whole (FullDOMConverter); when that cannot run, or
// cannot do better, the artifact is flagged PARTIAL where the reader sees it.

const (
	// recallFloorPercent is the share of the visible words an extraction must
	// keep. Visible text already excludes navigation, asides, footers and
	// forms, so an article extraction keeps well over half of what remains.
	recallFloorPercent = 25
	// recallMinVisibleWords keeps the gate off short pages, where a few words
	// of chrome swing the ratio and there is little to lose anyway.
	recallMinVisibleWords = 300
)

// partialMarkerPrefix opens the content of an artifact known to be
// incomplete. It is the ONE record of partiality: the cache stores it with the
// content, every surface that shows the content shows it, and Result.Partial
// is read back from it (partialReason).
const partialMarkerPrefix = "> **Partial artifact:** "

// lazyLoadMarker is the <meta name> the browser worker leaves in a render its
// scroll cap stopped while content was still arriving (browser.py
// LAZY_LOAD_MARKER).
const lazyLoadMarker = "harvester-lazy-load"

// lazyLoadTokenAttr is the attribute on that marker carrying the token the
// worker was sent (browser.py MARKER_TOKEN_ATTR).
const lazyLoadTokenAttr = "data-harvester-token"

// browserMarkerToken is this process's lazy-load marker token. The browser
// worker stamps it on the marker it leaves, and lazyLoadIncomplete reads back
// only a marker carrying it: a page cannot know it, so a page shipping a meta
// of the marker's name in its own markup never flags itself partial.
var browserMarkerToken = rand.Text()

// BrowserMarkerToken is the token a BrowserFetcher adapter sends the browser
// worker with every render.
func BrowserMarkerToken() string { return browserMarkerToken }

// withPartial prefixes content with the partial marker for reason; an empty
// reason returns content unflagged, and so does blank content: the marker is a
// note ABOUT an artifact, and nothing flagged must never read as something.
// The content is pageText first: only this function writes the marker.
func withPartial(content, reason string) string {
	content = pageText(content)
	reason = strings.Join(strings.Fields(reason), " ")
	if reason == "" || strings.TrimSpace(content) == "" {
		return content
	}
	return partialMarkerPrefix + reason + "\n\n" + content
}

// pageText is converted page text with the page's own copy of the partial
// marker defused: text that opens with partialMarkerPrefix gets its ">"
// escaped, so it reads as the page wrote it and is never read back as the
// harvester's flag. Every door converted text enters by passes it through:
// withPartial, convertFetchedDocument, the reader rungs and the OCR rescues.
func pageText(content string) string {
	trimmed := strings.TrimLeft(content, " \t\r\n")
	if !strings.HasPrefix(trimmed, partialMarkerPrefix) {
		return content
	}
	return content[:len(content)-len(trimmed)] + `\` + trimmed
}

// partialBody is content without its partial marker line — what the ladder's
// length gates measure, so the marker's own words never count as the page's.
func partialBody(content string) string {
	rest, ok := strings.CutPrefix(content, partialMarkerPrefix)
	if !ok {
		return content
	}
	_, body, _ := strings.Cut(rest, "\n")
	return body
}

// partialReason reads the partial marker back from content; "" when the
// artifact carries none.
func partialReason(content string) string {
	rest, ok := strings.CutPrefix(content, partialMarkerPrefix)
	if !ok {
		return ""
	}
	line, _, _ := strings.Cut(rest, "\n")
	return strings.TrimSpace(line)
}

// joinReasons joins the non-empty partial reasons into one line.
func joinReasons(reasons ...string) string {
	kept := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		if reason = strings.TrimSpace(reason); reason != "" {
			kept = append(kept, reason)
		}
	}
	return strings.Join(kept, "; ")
}

// errorReasonHTTPStatusRe pulls a bare "HTTP <code>" out of an error's own
// wording — the one fragment of it safe to keep verbatim.
var errorReasonHTTPStatusRe = regexp.MustCompile(`\bHTTP (\d{3})\b`)

// errorReasonClass is the ONE place a partial reason is built from an error:
// every producer of a gap (content.go's converters, loaders.go's requests)
// calls it instead of err.Error(). A local converter's error chain can carry
// a scratch-file path (harvestmcp's convertScratch input under $TMPDIR), a
// worker's stderr tail, or another host's filesystem layout — none of which
// may reach a partial artifact, the PARTIAL receipt header, or the cache. The
// class keeps only what is safe to repeat: cancelled, timeout, the worker's
// exit code, an HTTP status already embedded in the wording, or fallback —
// the caller's own short name for the step that failed. The error's full
// text still reaches the log (obs.FieldErr) at the call site; this function
// never sees or needs it to do that. The ONE named exception is
// loaders.go's graftErrorClass: a loader's graft failure already names page
// content read off the wire, safe to repeat rather than classify away, and
// bounded there so it can never leak more than graftErrorReasonMaxLen.
func errorReasonClass(err error, fallback string) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout"
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return fmt.Sprintf("worker exited %d", exitErr.ExitCode())
	}
	if m := errorReasonHTTPStatusRe.FindStringSubmatch(err.Error()); m != nil {
		return "HTTP " + m[1]
	}
	return fallback
}

// lazyLoadIncomplete returns the browser worker's incomplete-lazy-load note
// from doc, or "" when the render carried none. A marker without this
// process's token is the page's own markup, never the worker's note.
func lazyLoadIncomplete(doc *html.Node) string {
	var note string
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if note != "" {
			return
		}
		if node.Type == html.ElementNode && node.DataAtom == atom.Meta &&
			strings.EqualFold(nodeAttr(node, "name"), lazyLoadMarker) &&
			nodeAttr(node, lazyLoadTokenAttr) == browserMarkerToken {
			note = "lazy-loaded content " + strings.TrimSpace(nodeAttr(node, "content"))
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return note
}

// hiddenSubtree reports an element a reader never sees as page content: inert
// or non-text elements, page chrome (navigation, asides, footers, forms,
// controls, dialogs) and anything hidden.
func hiddenSubtree(node *html.Node) bool {
	switch node.DataAtom {
	case atom.Head, atom.Script, atom.Style, atom.Noscript, atom.Template, atom.Svg, atom.Math,
		atom.Iframe, atom.Object, atom.Canvas, atom.Nav, atom.Aside, atom.Footer, atom.Form,
		atom.Button, atom.Select, atom.Textarea, atom.Dialog:
		return true
	}
	if hasAttr(node, "hidden") {
		return true
	}
	if strings.EqualFold(nodeAttr(node, "aria-hidden"), "true") {
		return true
	}
	switch strings.ToLower(nodeAttr(node, "role")) {
	case "navigation", "banner", "contentinfo", "complementary", "dialog", "menu":
		return true
	}
	style := strings.ReplaceAll(strings.ToLower(nodeAttr(node, "style")), " ", "")
	return strings.Contains(style, "display:none") || strings.Contains(style, "visibility:hidden")
}

// hasAttr reports an attribute present on node, whatever its value (a
// boolean attribute such as hidden carries none).
func hasAttr(node *html.Node, key string) bool {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, key) {
			return true
		}
	}
	return false
}

// visibleWords returns the lower-cased words a reader sees in doc, in order.
func visibleWords(doc *html.Node) []string {
	var words []string
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && hiddenSubtree(node) {
			return
		}
		if node.Type == html.TextNode {
			words = append(words, shellWordRe.FindAllString(strings.ToLower(node.Data), -1)...)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return words
}

var (
	markdownImageSyntaxRe = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	markdownLinkTargetRe  = regexp.MustCompile(`\]\([^)]*\)`)
	bareURLRe             = regexp.MustCompile(`https?://\S+`)
)

// markdownWords returns the lower-cased words of converted markdown a reader
// reads: link targets, image syntax, bare URLs and any residual tags are not
// words of the page.
func markdownWords(markdown string) []string {
	text := markdownImageSyntaxRe.ReplaceAllString(markdown, " ")
	text = markdownLinkTargetRe.ReplaceAllString(text, "]")
	text = bareURLRe.ReplaceAllString(text, " ")
	text = htmlTagRe.ReplaceAllString(text, " ")
	return shellWordRe.FindAllString(strings.ToLower(text), -1)
}

// recallMeasure is one extraction measured against its page.
type recallMeasure struct {
	extracted int
	visible   int
}

func measureRecall(visible int, markdown string) recallMeasure {
	return recallMeasure{extracted: len(markdownWords(markdown)), visible: visible}
}

// measureContentRecall measures a WHOLE-DOM conversion: only its words that
// are the page's visible words count, each at most as often as the page shows
// it. A full-DOM conversion carries the page chrome the visible words exclude
// (navigation, footers, forms); counted by length, that chrome stands in for
// content the conversion dropped, and the fallback always reads complete.
func measureContentRecall(visible []string, markdown string) recallMeasure {
	remaining := make(map[string]int, len(visible))
	for _, word := range visible {
		remaining[word]++
	}
	kept := 0
	for _, word := range markdownWords(markdown) {
		if remaining[word] > 0 {
			remaining[word]--
			kept++
		}
	}
	return recallMeasure{extracted: kept, visible: len(visible)}
}

// low reports an extraction below the recall floor on a page long enough to
// judge.
func (measure recallMeasure) low() bool {
	return measure.visible >= recallMinVisibleWords && measure.extracted*100 < measure.visible*recallFloorPercent
}

func (measure recallMeasure) String() string {
	percent := 0
	if measure.visible > 0 {
		percent = measure.extracted * 100 / measure.visible
	}
	return fmt.Sprintf("%d of %d visible words (%d%%)", measure.extracted, measure.visible, percent)
}
