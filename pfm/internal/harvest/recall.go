package harvest

import (
	"fmt"
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

// withPartial prefixes content with the partial marker for reason; an empty
// reason returns content unchanged, and so does blank content: the marker is a
// note ABOUT an artifact, and nothing flagged must never read as something.
func withPartial(content, reason string) string {
	reason = strings.Join(strings.Fields(reason), " ")
	if reason == "" || strings.TrimSpace(content) == "" {
		return content
	}
	return partialMarkerPrefix + reason + "\n\n" + content
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

// lazyLoadIncomplete returns the browser worker's incomplete-lazy-load note
// from doc, or "" when the render carried none.
func lazyLoadIncomplete(doc *html.Node) string {
	var note string
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if note != "" {
			return
		}
		if node.Type == html.ElementNode && node.DataAtom == atom.Meta &&
			strings.EqualFold(nodeAttr(node, "name"), lazyLoadMarker) {
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

// markdownWordCount counts the words of converted markdown a reader reads:
// link targets, image syntax, bare URLs and any residual tags are not words of
// the page.
func markdownWordCount(markdown string) int {
	text := markdownImageSyntaxRe.ReplaceAllString(markdown, " ")
	text = markdownLinkTargetRe.ReplaceAllString(text, "]")
	text = bareURLRe.ReplaceAllString(text, " ")
	text = htmlTagRe.ReplaceAllString(text, " ")
	return len(shellWordRe.FindAllString(text, -1))
}

// recallMeasure is one extraction measured against its page.
type recallMeasure struct {
	extracted int
	visible   int
}

func measureRecall(visible int, markdown string) recallMeasure {
	return recallMeasure{extracted: markdownWordCount(markdown), visible: visible}
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
