package harvest

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// Inert containers hold markup a browser does not render as page content: a
// <template>'s content stays an inert fragment until script clones it, a
// <noscript>'s body is raw text in a scripting browser, and a JSON island is
// data. Server-rendered pages stream deferred or hydrated content through all
// three — a forum's whole comment tree inside a <template> that client script
// moves into <main> on hydration — and a main-content extractor skips them by
// spec, so the static page converts to its chrome. The pre-pass below surfaces
// every inert container that carries content the page does not already show,
// before extraction runs.

const (
	// inertMinWords separates deferred content from a UI stub (a tooltip
	// template, a "please enable JavaScript" notice).
	inertMinWords = 30
	// inertProbeWords is how many leading words decide that a container's
	// content is already visible (a <noscript> fallback of a rendered article).
	inertProbeWords = 12
	// unwrappedAttr marks each surfaced container with the kind it came from.
	unwrappedAttr = "data-harvest-unwrapped"
	// divTag is the element every surfaced container becomes.
	divTag = "div"
)

// unwrapInertContainers surfaces doc's inert containers in place and returns
// how many it surfaced. Templates and noscripts are only unwrapped inside
// <body>; a declarative shadow root (<template shadowrootmode>) is rendered
// content and is always unwrapped; JSON-LD islands contribute their
// articleBody/text strings as a section at the end of <body>.
func unwrapInertContainers(ctx context.Context, doc *html.Node) int {
	body := firstElement(doc, "body")
	if body == nil {
		return 0
	}
	// shown is every word the page shows so far; windows holds each
	// inertProbeWords-word run over it, so a probe is shown exactly when it is a
	// run of shown words — the word-boundary substring test, in linear time.
	shown := visibleWords(doc)
	windows := map[string]bool{}
	addWindows := func(from int) {
		for end := max(from, inertProbeWords-1); end < len(shown); end++ {
			windows[strings.Join(shown[end-inertProbeWords+1:end+1], " ")] = true
		}
	}
	addWindows(0)
	surface := func(words []string) bool {
		if len(words) < inertMinWords {
			return false
		}
		if windows[strings.Join(words[:inertProbeWords], " ")] {
			return false
		}
		start := len(shown)
		shown = append(shown, words...)
		addWindows(start)
		return true
	}
	count := 0
	// inSurfaced is set below a surfaced template: its words, a nested
	// template's included, already joined shown, so a nested container of
	// content surfaces with it rather than failing the probe on its own words.
	var walk func(node *html.Node, inSurfaced bool)
	walk = func(node *html.Node, inSurfaced bool) {
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if child.Type != html.ElementNode {
				continue
			}
			childSurfaced := inSurfaced
			switch child.DataAtom {
			case atom.Template:
				if hasAttr(child, "shadowrootmode") {
					retagUnwrapped(child, "template")
					count++
					break
				}
				if words := textWords(child); inSurfaced && len(words) >= inertMinWords || surface(words) {
					retagUnwrapped(child, "template")
					count++
					childSurfaced = true
				}
			case atom.Noscript:
				if unwrapNoscript(ctx, child, surface) {
					count++
				}
			}
			walk(child, childSurfaced)
		}
	}
	walk(body, false)
	if texts := jsonLDTexts(ctx, doc, surface); len(texts) > 0 {
		section := &html.Node{
			Type:     html.ElementNode,
			Data:     "section",
			DataAtom: atom.Section,
			Attr:     []html.Attribute{{Key: unwrappedAttr, Val: "json-ld"}},
		}
		for _, text := range texts {
			for _, paragraph := range strings.Split(text, "\n") {
				if paragraph = strings.TrimSpace(paragraph); paragraph == "" {
					continue
				}
				p := &html.Node{Type: html.ElementNode, Data: "p", DataAtom: atom.P}
				p.AppendChild(&html.Node{Type: html.TextNode, Data: paragraph})
				section.AppendChild(p)
			}
		}
		body.AppendChild(section)
		count += len(texts)
	}
	return count
}

// retagUnwrapped turns an inert container into a plain <div> that keeps its
// children, so every renderer and extractor treats them as page content.
func retagUnwrapped(node *html.Node, kind string) {
	node.Data = divTag
	node.DataAtom = atom.Div
	node.Attr = []html.Attribute{{Key: unwrappedAttr, Val: kind}}
}

// textWords returns every lower-cased word under node, script and style
// bodies excluded — the content a container would show once rendered.
func textWords(node *html.Node) []string {
	var words []string
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.ElementNode && (current.DataAtom == atom.Script || current.DataAtom == atom.Style) {
			return
		}
		if current.Type == html.TextNode {
			words = append(words, shellWordRe.FindAllString(strings.ToLower(current.Data), -1)...)
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return words
}

// unwrapNoscript re-parses a <noscript>'s raw text as markup and, when it
// carries content worth surfacing, replaces the text with that markup.
func unwrapNoscript(ctx context.Context, node *html.Node, surface func([]string) bool) bool {
	var raw strings.Builder
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.TextNode {
			raw.WriteString(child.Data)
		}
	}
	if strings.TrimSpace(raw.String()) == "" {
		return false
	}
	parent := &html.Node{Type: html.ElementNode, Data: divTag, DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(raw.String()), parent)
	if err != nil {
		obs.Logger(ctx).Warn("harvest: <noscript> content could not be parsed; left inert", obs.FieldErr, err.Error())
		return false
	}
	holder := &html.Node{Type: html.ElementNode, Data: divTag, DataAtom: atom.Div}
	for _, parsed := range nodes {
		holder.AppendChild(parsed)
	}
	if !surface(textWords(holder)) {
		return false
	}
	for child := node.FirstChild; child != nil; {
		next := child.NextSibling
		node.RemoveChild(child)
		child = next
	}
	for child := holder.FirstChild; child != nil; {
		next := child.NextSibling
		holder.RemoveChild(child)
		node.AppendChild(child)
		child = next
	}
	retagUnwrapped(node, "noscript")
	return true
}

// jsonLDTexts returns the articleBody/text strings of doc's JSON-LD islands
// that surface() accepts. A malformed island is logged and skipped — it is
// data the page ships, not an input this pass owns.
func jsonLDTexts(ctx context.Context, doc *html.Node, surface func([]string) bool) []string {
	var texts []string
	var collect func(value any)
	collect = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys) // one page, one artifact: never map order
			for _, key := range keys {
				field := typed[key]
				if text, ok := field.(string); ok && (key == "articleBody" || key == "text") {
					if surface(shellWordRe.FindAllString(strings.ToLower(text), -1)) {
						texts = append(texts, text)
					}
					continue
				}
				collect(field)
			}
		case []any:
			for _, item := range typed {
				collect(item)
			}
		}
	}
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.DataAtom == atom.Script &&
			strings.EqualFold(strings.TrimSpace(nodeAttr(node, "type")), "application/ld+json") {
			if node.FirstChild != nil {
				var value any
				if err := json.Unmarshal([]byte(node.FirstChild.Data), &value); err != nil {
					obs.Logger(ctx).
						Warn("harvest: JSON-LD island is not valid JSON; skipped", obs.FieldErr, err.Error())
				} else {
					collect(value)
				}
			}
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return texts
}
