package harvest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// Per-site tree extractors. A generic main-content extractor reduces a page
// to its article; where a site's structure is KNOWN — a discussion thread's
// post and nested comments — a structure-aware extractor renders the tree
// instead, and reconciles what it rendered against the count the page itself
// states. Every other site keeps the generic path. To add a site, add one
// entry here: the hosts it owns — or, for software any domain runs, a detect
// function that knows its pages by their markup — and an extract function that answers false
// for any page that is not the shape it knows (a listing, a wall), so that
// page falls through to the generic path — and, when its pages hold loaders
// for the rest of the tree, a loaders function Go follows them by
// (loaders.go) before the extract function reads the page.

// siteExtraction is one structure-aware rendering of a page. partial names
// what the page could not supply (loaders still unexpanded and what they
// hide); "" when the rendering is complete.
// renderMayComplete is set when at least one gap is a loader a browser render
// presses, so the browser rung can close it. apiRecord is set when the
// rendering holds at least one record the site's API answered and the
// extractor proved this thread's (honoured only for an extractor that
// readsSiteAPI). unrendered, answered with ok false, is what an extractor
// that knows the page but could not render it leaves out (its API record did
// not load): the page falls through to another path, which names it. stated,
// set beside unrendered, reads from the content a rung stores the count the
// page states of what was not loaded (booking.go).
type siteExtraction struct {
	markdown          string
	partial           string
	renderMayComplete bool
	apiRecord         bool
	unrendered        string
	stated            func(content string) string
}

// unknownAuthor stands, in every extractor's rendering, for a post or comment
// that names no author.
const unknownAuthor = "[unknown]"

// unknownPosted stands for the date of a post or comment that names none.
const unknownPosted = unknownAuthor

type siteExtractor struct {
	name  string
	hosts []string
	// detect, when set, claims a page on any host by its markup (a forum
	// engine's generator meta); such an extractor's loaders may only request
	// the page's own host.
	detect func(doc *html.Node) bool
	// paths, when set, narrows the host claim to the addresses it accepts (a
	// host whose pages two extractors split); a loader's request is still
	// judged by the host alone.
	paths   func(page *url.URL) bool
	extract func(doc *html.Node, source *url.URL) (siteExtraction, bool)
	// loaders names the loaders still in a page of this site (loaders.go),
	// in DOM order; nil when the site has none Go can follow.
	loaders func(doc *html.Node, source *url.URL) []pageLoader
	// loaderCap, when set, raises the fetch's loader request cap for this
	// site past loaderRequestCap (a site whose loaders each answer only a
	// few items); the pace and the 429 and wall stops hold as they are.
	loaderCap int
	// pressLoaders: the browser rung may press this site's load-more buttons.
	pressLoaders bool
	// readsSiteAPI: the extractor renders the thread from its site's API, never
	// from the page's markup, so a page it renders from an API record is
	// content even where the site served a wall in its place (harvest.go).
	// Every markup extractor leaves it unset and keeps the wall guard.
	readsSiteAPI bool
}

var siteExtractors = []siteExtractor{
	{
		name:         "reddit-thread",
		hosts:        []string{"reddit.com"},
		extract:      extractRedditThread,
		loaders:      redditLoaders,
		loaderCap:    redditLoaderCap,
		pressLoaders: true,
	},
	{
		name:    "hacker-news-thread",
		hosts:   []string{hnHost},
		extract: extractHNThread,
		loaders: hnLoaders,
	},
	{
		name:    "github-discussion",
		hosts:   []string{githubHost},
		paths:   isGitHubDiscussionAddress,
		extract: extractGitHubDiscussion,
		loaders: githubDiscussionLoaders,
	},
	{
		name:         "github-issue",
		hosts:        []string{githubHost},
		extract:      extractGitHubIssue,
		loaders:      githubLoaders,
		readsSiteAPI: true,
	},
	{
		name:         "stackexchange-listing",
		hosts:        stackExchangeHosts,
		paths:        isStackExchangeListingAddress,
		extract:      extractStackExchangeListing,
		loaders:      stackExchangeListingLoaders,
		readsSiteAPI: true,
	},
	{
		name:         "stackexchange-question",
		hosts:        stackExchangeHosts,
		extract:      extractStackExchangeQuestion,
		loaders:      stackExchangeLoaders,
		readsSiteAPI: true,
	},
	{
		name:         "bluesky-post",
		hosts:        []string{bskyHost, bskyAPIHost},
		extract:      extractBlueskyPost,
		loaders:      bskyLoaders,
		readsSiteAPI: true,
	},
	{
		name:         "mastodon-status",
		detect:       isMastodon,
		extract:      extractMastodonStatus,
		loaders:      mastodonLoaders,
		readsSiteAPI: true,
	},
	{
		name:         "gitlab-item",
		hosts:        []string{gitlabComHost},
		detect:       isGitLab,
		extract:      extractGitLabItem,
		loaders:      gitlabLoaders,
		readsSiteAPI: true,
	},
	{
		name:         "devto-article",
		hosts:        []string{devtoHost},
		extract:      extractDevtoArticle,
		loaders:      devtoLoaders,
		readsSiteAPI: true,
	},
	{
		name:         "lemmy-post",
		hosts:        lemmyHosts,
		detect:       isLemmy,
		extract:      extractLemmyPost,
		loaders:      lemmyLoaders,
		readsSiteAPI: true,
	},
	{
		name:         "substack-post",
		detect:       isSubstack,
		extract:      extractSubstackPost,
		loaders:      substackLoaders,
		readsSiteAPI: true,
	},
	{
		name:         "youtube-video",
		hosts:        []string{youtubeHost},
		paths:        isYouTubeWatch,
		extract:      extractYouTubeVideo,
		loaders:      youtubeLoaders,
		readsSiteAPI: true,
	},
	{
		name:         "steam-app",
		hosts:        []string{steamHost},
		paths:        isSteamApp,
		extract:      extractSteamApp,
		loaders:      steamLoaders,
		readsSiteAPI: true,
	},
	{
		name:         "producthunt-reviews",
		hosts:        []string{"producthunt.com"},
		paths:        isProductHuntReviews,
		extract:      extractProductHuntReviews,
		loaders:      productHuntLoaders,
		readsSiteAPI: true,
	},
	{
		name:         "notion-page",
		hosts:        notionHosts,
		paths:        isNotionPage,
		extract:      extractNotionPage,
		loaders:      notionLoaders,
		readsSiteAPI: true,
	},
	{
		name:    "slashdot-story",
		hosts:   []string{slashdotHost},
		paths:   isSlashdotStory,
		extract: extractSlashdotStory,
		loaders: slashdotLoaders,
	},
	{
		name:         "imdb-reviews",
		hosts:        []string{"imdb.com"},
		paths:        isIMDbReviews,
		extract:      extractIMDbReviews,
		loaders:      imdbLoaders,
		readsSiteAPI: true,
	},
	{
		name:    "lobsters-story",
		hosts:   []string{lobstersHost},
		paths:   isLobstersStory,
		extract: extractLobstersStory,
	},
	{
		name:    "booking-reviews",
		hosts:   []string{"booking.com"},
		paths:   isBookingHotel,
		extract: extractBookingReviews,
	},
	{
		name:    "discourse-topic",
		detect:  isDiscourse,
		extract: extractDiscourseTopic,
		loaders: discourseLoaders,
	},
}

// ownsHost reports whether extractor owns host (lowercased): the host itself
// or any subdomain of it.
func (extractor siteExtractor) ownsHost(host string) bool {
	for _, owned := range extractor.hosts {
		if host == owned || strings.HasSuffix(host, "."+owned) {
			return true
		}
	}
	return false
}

// claims reports whether extractor owns page: by its host, or by its markup.
func (extractor siteExtractor) claims(page *url.URL, doc *html.Node) bool {
	if extractor.ownsHost(strings.ToLower(page.Hostname())) && (extractor.paths == nil || extractor.paths(page)) {
		return true
	}
	return extractor.detect != nil && extractor.detect(doc)
}

// mayRequest reports whether a loader of page may request target: a host the
// extractor owns, or — for an extractor that knows pages by their markup —
// the page's own host.
func (extractor siteExtractor) mayRequest(page, target *url.URL) bool {
	host := strings.ToLower(target.Hostname())
	if extractor.ownsHost(host) {
		return true
	}
	return extractor.detect != nil && (target.Scheme == schemeHTTPS || target.Scheme == schemeHTTP) &&
		host == strings.ToLower(page.Hostname())
}

// SitePressesLoaders reports whether the browser rung may press source's
// load-more buttons: true only when a registered extractor owning the host
// asks for it. Pressing can fire requests or navigation, so every other page
// (and an unparsable URL) is rendered read-only. The rule is the host alone:
// the rung asks before any markup is read, so an extractor that claims pages
// by their markup (detect) may not set pressLoaders — a registry test refuses
// that entry, which would otherwise never press.
func SitePressesLoaders(source string) bool {
	parsed, err := url.Parse(source)
	if err != nil {
		obs.Logger(context.Background()).Warn("harvest: a fetch source could not be parsed; loader pressing left off",
			"source", logSource(source), obs.FieldErr, err.Error())
		return false
	}
	if parsed.Host == "" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	for _, extractor := range siteExtractors {
		if extractor.pressLoaders && extractor.ownsHost(host) {
			return true
		}
	}
	return false
}

// followForSite follows, in doc, the loaders of the extractor that claims the
// page (loaders.go), and returns what the following left in doc. A nil
// budget (a conversion outside the web ladder) and a page no extractor names
// loaders for leave doc as it is, with nothing reported left.
func (h *Harvester) followForSite(
	ctx context.Context,
	source string,
	doc *html.Node,
	budget *loaderBudget,
) loaderRemainder {
	if budget == nil {
		return loaderRemainder{}
	}
	parsed, err := url.Parse(source)
	if err != nil {
		obs.Logger(ctx).Warn("harvest: a fetch source could not be parsed; no loaders followed",
			"source", logSource(source), obs.FieldErr, err.Error())
		return loaderRemainder{}
	}
	if parsed.Host == "" {
		return loaderRemainder{}
	}
	for _, extractor := range siteExtractors {
		if extractor.loaders == nil || !extractor.claims(parsed, doc) {
			continue
		}
		before := budget.requests
		budget.limit = max(budget.limit, extractor.loaderCap)
		rest := h.followLoaders(ctx, doc, parsed, extractor, budget)
		if budget.requests > before || budget.stopped != "" {
			state := fmt.Sprintf("%d followed, %d failed in the fetch", len(budget.followed), len(budget.failures))
			obs.Logger(ctx).Info("harvest: loaders followed",
				"kind", extractor.name,
				"target", logSource(source),
				"count", budget.requests-before,
				"state", state,
				"reason", budget.stopped)
		}
		return rest
	}
	return loaderRemainder{}
}

// extractForSite runs the extractor that claims the page. ok is false when no
// extractor claims it or the page is not the shape it knows; the extraction
// then carries, in unrendered, what an extractor that knew the page could not
// render. apiRecord survives only from an extractor that readsSiteAPI.
func extractForSite(source string, doc *html.Node) (siteExtraction, string, bool) {
	parsed, err := url.Parse(source)
	if err != nil {
		obs.Logger(context.Background()).Warn("harvest: a fetch source could not be parsed; no site extractor tried",
			"source", logSource(source), obs.FieldErr, err.Error())
		return siteExtraction{}, "", false
	}
	if parsed.Host == "" {
		return siteExtraction{}, "", false
	}
	named := siteExtraction{}
	for _, extractor := range siteExtractors {
		if !extractor.claims(parsed, doc) {
			continue
		}
		extraction, ok := extractor.extract(doc, parsed)
		if ok {
			extraction.apiRecord = extraction.apiRecord && extractor.readsSiteAPI
			return extraction, extractor.name, true
		}
		named.unrendered = joinReasons(named.unrendered, extraction.unrendered)
		if named.stated == nil {
			named.stated = extraction.stated
		}
	}
	return named, "", false
}

// keptAnswers returns the elements named tag in doc: the API answers an
// extractor that reads its site's API keeps in the page (keepAnswer).
func keptAnswers(doc *html.Node, tag string) []*html.Node {
	var found []*html.Node
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if isElement(node, tag) {
			found = append(found, node)
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return found
}

// keepAnswer appends one API answer to doc's body as a tag element carrying
// the loader's key, the answer's kind and page number — or, with body nil, a
// dropped loader's mark — so a later conversion replays it like any followed
// loader.
func keepAnswer(doc *html.Node, tag, key, kind string, number int, body []byte) {
	parent := firstElement(doc, "body")
	if parent == nil {
		parent = doc
	}
	node := &html.Node{
		Type: html.ElementNode,
		Data: tag,
		Attr: []html.Attribute{
			{Key: "key", Val: key},
			{Key: "kind", Val: kind},
			{Key: "page", Val: strconv.Itoa(number)},
		},
	}
	if body == nil {
		node.Attr = append(node.Attr, html.Attribute{Key: "dropped", Val: "true"})
	} else {
		node.AppendChild(&html.Node{Type: html.TextNode, Data: string(body)})
	}
	parent.AppendChild(node)
}

// keptPages decodes the paged API answers kept in doc under tag, in page
// order, stopping (logged, naming site) at one that no longer decodes or is
// out of order; dropped reports a dropped loader's mark.
func keptPages[T any](doc *html.Node, tag, site string) (pages []T, dropped bool) {
	for _, node := range keptAnswers(doc, tag) {
		if nodeAttr(node, "dropped") != "" {
			dropped = true
			continue
		}
		var answer T
		if err := json.Unmarshal([]byte(rawText(node)), &answer); err != nil {
			obs.Logger(context.Background()).Warn("harvest: a kept "+site+" answer no longer decodes; left out",
				"page", nodeAttr(node, "page"), obs.FieldErr, err.Error())
			break
		}
		if number, err := strconv.Atoi(nodeAttr(node, "page")); err != nil || number != len(pages) {
			obs.Logger(context.Background()).Warn("harvest: a kept "+site+" answer is out of page order; left out",
				"page", nodeAttr(node, "page"))
			break
		}
		pages = append(pages, answer)
	}
	return pages, dropped
}

// isElement reports an element node with the given (custom) tag name.
func isElement(node *html.Node, tag string) bool {
	return node != nil && node.Type == html.ElementNode && node.Data == tag
}

// firstWithAttr returns the first element under node (node included) whose
// attribute key equals value, not descending into elements stop() refuses.
func firstWithAttr(node *html.Node, key, value string, stop func(*html.Node) bool) *html.Node {
	if node.Type == html.ElementNode && nodeAttr(node, key) == value {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && stop != nil && stop(child) {
			continue
		}
		if found := firstWithAttr(child, key, value, stop); found != nil {
			return found
		}
	}
	return nil
}

// markdownBlocks renders a rich-text subtree (a post body, a comment body) as
// Markdown blocks: paragraphs, headings, lists, quotes, code, links. Controls,
// scripts, styles and inert templates are not text.
type markdownRenderer struct {
	base *url.URL
	// textOnly, when set, names links the site injects into user text (a
	// highlighted search term); they render as their text alone.
	textOnly func(link *url.URL) bool
}

// lineBreak survives whitespace collapsing inside a paragraph as a hard break.
const lineBreak = "\x00"

func (renderer markdownRenderer) blocks(node *html.Node) []string {
	var out []string
	var paragraph strings.Builder
	flush := func() {
		text := strings.Join(strings.Fields(paragraph.String()), " ")
		text = strings.TrimSpace(
			strings.ReplaceAll(strings.ReplaceAll(text, " "+lineBreak, lineBreak), lineBreak+" ", lineBreak),
		)
		text = strings.Trim(strings.ReplaceAll(text, lineBreak, "  \n"), " \n")
		if text != "" {
			out = append(out, text)
		}
		paragraph.Reset()
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.TextNode {
			paragraph.WriteString(child.Data)
			continue
		}
		if child.Type != html.ElementNode || skippedInMarkdown(child) {
			continue
		}
		switch child.DataAtom {
		case atom.P, atom.Div, atom.Section, atom.Article, atom.Figure, atom.Figcaption, atom.Table,
			atom.Thead, atom.Tbody, atom.Tfoot, atom.Details, atom.Summary:
			flush()
			out = append(out, renderer.blocks(child)...)
		case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
			flush()
			if text := renderer.inline(child); text != "" {
				level, _ := strconv.Atoi(child.Data[1:])
				out = append(out, strings.Repeat("#", level)+" "+text)
			}
		case atom.Ul, atom.Ol:
			flush()
			if list := renderer.list(child, child.DataAtom == atom.Ol); list != "" {
				out = append(out, list)
			}
		case atom.Blockquote:
			flush()
			if inner := strings.Join(renderer.blocks(child), "\n\n"); inner != "" {
				out = append(out, prefixLines(inner, "> ", ">"))
			}
		case atom.Pre:
			flush()
			out = append(out, "```\n"+strings.Trim(rawText(child), "\n")+"\n```")
		case atom.Hr:
			flush()
			out = append(out, "---")
		case atom.Tr:
			flush()
			var cells []string
			for cell := child.FirstChild; cell != nil; cell = cell.NextSibling {
				if cell.Type == html.ElementNode && (cell.DataAtom == atom.Td || cell.DataAtom == atom.Th) {
					cells = append(cells, renderer.inline(cell))
				}
			}
			out = append(out, "| "+strings.Join(cells, " | ")+" |")
		default:
			paragraph.WriteString(renderer.inlineElement(child))
		}
	}
	flush()
	return out
}

func (renderer markdownRenderer) list(node *html.Node, ordered bool) string {
	var items []string
	index := 0
	for item := node.FirstChild; item != nil; item = item.NextSibling {
		if item.Type != html.ElementNode || item.DataAtom != atom.Li {
			continue
		}
		index++
		marker := "- "
		if ordered {
			marker = strconv.Itoa(index) + ". "
		}
		body := strings.Join(renderer.blocks(item), "\n\n")
		items = append(items, marker+indentAfterFirst(body, strings.Repeat(" ", len(marker))))
	}
	return strings.Join(items, "\n")
}

func (renderer markdownRenderer) inline(node *html.Node) string {
	var text strings.Builder
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		switch {
		case child.Type == html.TextNode:
			text.WriteString(child.Data)
		case child.Type == html.ElementNode && !skippedInMarkdown(child):
			text.WriteString(renderer.inlineElement(child))
		}
	}
	return strings.Join(strings.Fields(strings.ReplaceAll(text.String(), lineBreak, " ")), " ")
}

func (renderer markdownRenderer) inlineElement(node *html.Node) string {
	switch node.DataAtom {
	case atom.Br:
		return lineBreak
	case atom.A:
		text := renderer.inline(node)
		href := renderer.resolve(nodeAttr(node, "href"))
		if renderer.textOnly != nil && href != "" {
			if parsed, err := url.Parse(href); err == nil && renderer.textOnly(parsed) {
				return text
			}
		}
		switch {
		case href == "":
			return text
		case text == "":
			return href
		}
		return "[" + text + "](" + href + ")"
	case atom.Strong, atom.B:
		return wrapInline(renderer.inline(node), "**")
	case atom.Em, atom.I:
		return wrapInline(renderer.inline(node), "*")
	case atom.Code:
		return wrapInline(rawText(node), "`")
	case atom.Del, atom.S:
		return wrapInline(renderer.inline(node), "~~")
	case atom.Img:
		if src := renderer.resolve(nodeAttr(node, "src")); src != "" {
			return "![" + nodeAttr(node, "alt") + "](" + src + ")"
		}
		return ""
	}
	// Unknown and custom inline elements (spans, emotes, number widgets)
	// contribute their text; a block nested inside one keeps its separation.
	var text strings.Builder
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		switch {
		case child.Type == html.TextNode:
			text.WriteString(child.Data)
		case child.Type == html.ElementNode && !skippedInMarkdown(child):
			if child.DataAtom == atom.P || child.DataAtom == atom.Div {
				text.WriteString(
					" " + lineBreak + lineBreak + strings.Join(renderer.blocks(child), lineBreak+lineBreak) + " ",
				)
				continue
			}
			text.WriteString(renderer.inlineElement(child))
		}
	}
	return text.String()
}

func (renderer markdownRenderer) resolve(href string) string {
	href = strings.TrimSpace(href)
	if href == "" || strings.HasPrefix(href, "#") || strings.HasPrefix(strings.ToLower(href), "javascript:") {
		return ""
	}
	parsed, err := url.Parse(href)
	if err != nil {
		obs.Logger(context.Background()).Warn("harvest: a link's href could not be parsed; left unresolved",
			"href", logSource(href), obs.FieldErr, err.Error())
		return href
	}
	if renderer.base != nil {
		return renderer.base.ResolveReference(parsed).String()
	}
	return parsed.String()
}

func skippedInMarkdown(node *html.Node) bool {
	switch node.DataAtom {
	case atom.Script, atom.Style, atom.Template, atom.Button, atom.Svg, atom.Noscript, atom.Input, atom.Form:
		return true
	}
	return false
}

func wrapInline(text, mark string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return mark + text + mark
}

// rawText is node's text with its whitespace intact (code, preformatted).
func rawText(node *html.Node) string {
	var text strings.Builder
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.TextNode {
			text.WriteString(current.Data)
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return text.String()
}

// prefixLines prefixes every line of text; blank lines get blankPrefix.
func prefixLines(text, prefix, blankPrefix string) string {
	lines := strings.Split(text, "\n")
	for index, line := range lines {
		if strings.TrimSpace(line) == "" {
			lines[index] = blankPrefix
		} else {
			lines[index] = prefix + line
		}
	}
	return strings.Join(lines, "\n")
}

// indentAfterFirst indents every line after the first; blank lines stay blank.
func indentAfterFirst(text, indent string) string {
	lines := strings.Split(text, "\n")
	for index := 1; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) != "" {
			lines[index] = indent + lines[index]
		}
	}
	return strings.Join(lines, "\n")
}
