package harvest

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// The Reddit thread extractor. A thread page (server-rendered or hydrated in
// the browser rung) carries the post as <shreddit-post> and every comment as a
// nested <shreddit-comment> whose attributes hold its author, score, depth and
// id, and whose slot="comment" child holds its body. Branches the page did not
// load sit behind <faceplate-partial src="/svc/shreddit/more-comments/...">
// loaders ("N more replies", "View more comments"), and a reply chain past the
// page's depth limit behind a link to the comment's own page ("Continue this
// thread"; "N more replies" in a continued page). Every comment carries that
// link in a fold-more block, hidden (class "hidden") while its replies are in
// the page — not a gap; a comment at the fold of a loader's answer shows it
// instead of its replies, and a shown link is a gap. redditLoaders names both
// kinds for Go to follow (loaders.go): a loader is POSTed its own form with the
// thread as Referer and answers a fragment of the tree, grafted in the
// loader's place; a link's page holds the comment again, and its replies are
// grafted in the link's place. The extractor then renders the post and the
// tree in DOM order and reconciles the comments it rendered against the count
// the thread states. The stated count includes comments Reddit does not
// serve (removed by its filters, deleted without a placeholder): with no
// loader or link left in the page, the remainder is named as that; while one
// is left, the page is partial and every gap is named.

// redditLoaderCap raises the loader requests one Reddit fetch may spend past
// loaderRequestCap: each loader answers one cursor's page of the tree (a few
// comments; the old-Reddit morechildren batch API asks for a login, the JSON
// API refuses), so a thread of a few thousand comments needs several hundred.
// At the kept loaderPace plus Reddit's answer time (about 1.3 s a request)
// the first ~150 requests take about 3 minutes; past them Reddit's quota
// (200 requests a 10-minute window, loader_quota.go) paces the rest to about
// 3 s each, within loaderPacingBudget: past it the following stops and the
// partial names when the rest may be read.
const redditLoaderCap = 1000

// redditQuota is the request quota Reddit states for its comment loaders.
var redditQuota = siteQuota{site: "Reddit", requests: 200, window: 10 * time.Minute}

var redditReplyCountRe = regexp.MustCompile(`(\d[\d,]*)\s+more\s+repl`)

// redditPartialAccept asks a loader for its fragment of the tree alone, as the
// page's own loader element does; without it Reddit answers the whole page.
const redditPartialAccept = "text/vnd.reddit.partial+html, text/html;q=0.9"

// redditComment is one rendered comment line block.
type redditComment struct {
	level   int
	author  string
	score   string
	created string
	blocks  []string
	status  string // "", "deleted" or "removed"
}

func extractRedditThread(doc *html.Node, _ *url.URL) (siteExtraction, bool) {
	post := redditThreadPost(doc)
	if post == nil {
		return siteExtraction{}, false
	}
	base := redditBase()
	// Reddit links highlighted terms in comments to a search page whose URL
	// carries per-render session ids and whose choice of term varies between
	// renders; kept, they make one thread two different artifacts.
	renderer := markdownRenderer{base: base, textOnly: func(link *url.URL) bool {
		host := strings.ToLower(link.Hostname())
		ownHost := host == "reddit.com" || strings.HasSuffix(host, ".reddit.com")
		return ownHost && strings.HasPrefix(link.Path, "/search")
	}}

	stated, statedKnown := redditCount(nodeAttr(post, "comment-count"))
	if !statedKnown {
		if stats := firstElement(doc, "shreddit-comment-tree-stats"); stats != nil {
			stated, statedKnown = redditCount(nodeAttr(stats, "total-comments"))
		}
	}

	var comments []redditComment
	seen := map[string]bool{}
	seenLoaders := map[string]bool{}
	continueLinks := map[string]bool{}
	replyLoaders, hiddenReplies, commentLoaders := 0, 0, 0
	var walk func(node *html.Node, level int, hidden bool)
	walk = func(node *html.Node, level int, hidden bool) {
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if child.Type != html.ElementNode {
				continue
			}
			childHidden := hidden || redditHiddenCopy(child)
			switch {
			case isElement(child, "head"):
				continue
			case redditContinueLink(child, childHidden):
				continueLinks[nodeAttr(child, "href")] = true
				continue
			case isElement(child, "shreddit-comment"):
				id := nodeAttr(child, "thingid")
				if id == "" || !seen[id] {
					if id != "" {
						seen[id] = true
					}
					comments = append(comments, redditRenderComment(child, level, renderer))
				}
				walk(child, level+1, childHidden)
				continue
			case redditMoreComments(child):
				key := redditLoaderKey(child)
				if seenLoaders[key] {
					continue
				}
				seenLoaders[key] = true
				label := strings.ToLower(nodeText(child))
				if match := redditReplyCountRe.FindStringSubmatch(label); match != nil {
					replyLoaders++
					count, _ := redditCount(match[1])
					hiddenReplies += count
				} else {
					commentLoaders++
				}
				continue
			}
			walk(child, level, childHidden)
		}
	}
	walk(doc, 0, false)

	deleted, removed := 0, 0
	for _, comment := range comments {
		switch comment.status {
		case "deleted":
			deleted++
		case "removed":
			removed++
		}
	}

	var gaps []string
	if replyLoaders > 0 {
		gaps = append(gaps, fmt.Sprintf("%d behind %d unexpanded \"more replies\"", hiddenReplies, replyLoaders))
	}
	if commentLoaders > 0 {
		gaps = append(gaps, fmt.Sprintf("%d unexpanded \"View more comments\" loader(s)", commentLoaders))
	}
	if len(continueLinks) > 0 {
		gaps = append(gaps, fmt.Sprintf(
			"%d \"Continue this thread\" link(s) not followed (those replies load on a separate page)",
			len(continueLinks),
		))
	}
	// unserved is the part of the stated count no loader or link in the page
	// leads to — derived only once none is left, never while one could still
	// load comments of an unknown number.
	unserved := 0
	if statedKnown {
		rest := stated - len(comments)
		switch {
		case len(gaps) == 0:
			unserved = max(rest, 0)
		case rest-hiddenReplies > 0:
			gaps = append(gaps, fmt.Sprintf(
				"%d not in the page (behind a loader or link of unknown size, or deleted/removed without a stub)",
				rest-hiddenReplies,
			))
		}
	}

	statedText := "unstated"
	if statedKnown {
		statedText = strconv.Itoa(stated)
	}
	countLine := fmt.Sprintf(
		"**Comments:** %s stated · %d loaded (%d deleted, %d removed)",
		statedText,
		len(comments),
		deleted,
		removed,
	)
	if unserved > 0 {
		countLine += fmt.Sprintf(
			" · %d not served by Reddit (removed by its filters, or deleted without a placeholder)",
			unserved,
		)
	}
	if len(gaps) > 0 {
		countLine += " · gaps: " + strings.Join(gaps, "; ")
	}

	var out strings.Builder
	title := strings.TrimSpace(nodeAttr(post, "post-title"))
	if title == "" {
		if heading := firstWithAttr(post, "slot", "title", nil); heading != nil {
			title = nodeText(heading)
		}
	}
	out.WriteString("# " + title + "\n\n")
	var meta []string
	if subreddit := nodeAttr(post, "subreddit-prefixed-name"); subreddit != "" {
		meta = append(meta, "**Subreddit:** "+subreddit)
	}
	if author := nodeAttr(post, "author"); author != "" {
		meta = append(meta, "**Author:** u/"+author)
	}
	if score := nodeAttr(post, "score"); score != "" {
		meta = append(meta, "**Score:** "+score)
	}
	if created := redditDate(nodeAttr(post, "created-timestamp")); created != "" {
		meta = append(meta, "**Posted:** "+created)
	}
	if len(meta) > 0 {
		out.WriteString(strings.Join(meta, " · ") + "  \n")
	}
	permalink := renderer.resolve(nodeAttr(post, "permalink"))
	if permalink != "" {
		out.WriteString("**Thread:** " + permalink + "  \n")
	}
	if link := renderer.resolve(nodeAttr(post, "content-href")); link != "" && link != permalink {
		out.WriteString("**Link:** " + link + "  \n")
	}
	out.WriteString(countLine + "\n\n")
	if body := firstWithAttr(post, "slot", "text-body", nil); body != nil {
		if blocks := renderer.blocks(body); len(blocks) > 0 {
			out.WriteString(strings.Join(blocks, "\n\n") + "\n\n")
		}
	}
	out.WriteString("---\n\n## Comments\n\n")
	if len(comments) == 0 {
		out.WriteString("*No comments are in this page.*\n")
	}
	for _, comment := range comments {
		out.WriteString(comment.markdown())
	}

	var counted *commentCount
	if statedKnown {
		counted = &commentCount{loaded: len(comments), stated: stated}
	}
	partial := ""
	if len(gaps) > 0 {
		partial = fmt.Sprintf(
			"reddit thread: %d of %s comments loaded — %s",
			len(comments),
			statedText,
			strings.Join(gaps, "; "),
		)
	}
	return siteExtraction{
		markdown:          out.String(),
		partial:           partial,
		comments:          counted,
		renderMayComplete: replyLoaders+commentLoaders > 0,
	}, true
}

// redditThreadPost returns the thread's own <shreddit-post>: the first one
// outside a <shreddit-feed>. A listing (a subreddit, a profile, the front
// page) carries its posts only as feed cards, so it has none and takes the
// generic path instead of reading its first card as a comment-less thread.
func redditThreadPost(node *html.Node) *html.Node {
	if isElement(node, "shreddit-feed") {
		return nil
	}
	if isElement(node, "shreddit-post") {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := redditThreadPost(child); found != nil {
			return found
		}
	}
	return nil
}

// redditRenderComment reads one <shreddit-comment>: its own attributes and
// its own body, never a nested reply's.
func redditRenderComment(node *html.Node, level int, renderer markdownRenderer) redditComment {
	comment := redditComment{
		level:   level,
		author:  nodeAttr(node, "author"),
		score:   nodeAttr(node, "score"),
		created: redditDate(nodeAttr(node, "created")),
	}
	notReply := func(child *html.Node) bool { return isElement(child, "shreddit-comment") }
	if body := firstWithAttr(node, "slot", "comment", notReply); body != nil {
		comment.blocks = renderer.blocks(body)
	}
	bodyText := strings.ToLower(strings.TrimSpace(strings.Join(comment.blocks, " ")))
	ownText := strings.ToLower(redditOwnText(node))
	// The stub phrases are only read off a comment with no body of its own —
	// a reply QUOTING "removed by moderator" is not itself removed.
	stub := bodyText == ""
	switch {
	case bodyText == "[removed]" ||
		stub && (strings.Contains(ownText, "removed by moderator") || strings.Contains(ownText, "removed by reddit")):
		comment.status = "removed"
	case comment.author == "[deleted]" || bodyText == "[deleted]" || stub && strings.Contains(ownText, "deleted by user"):
		comment.status = "deleted"
	}
	return comment
}

// redditOwnText is a comment's text without its replies' text.
func redditOwnText(node *html.Node) string {
	var parts []string
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			switch {
			case child.Type == html.TextNode:
				parts = append(parts, child.Data)
			case isElement(child, "shreddit-comment"):
				continue
			case child.Type == html.ElementNode:
				walk(child)
			}
		}
	}
	walk(node)
	return strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
}

func (comment redditComment) markdown() string {
	indent := strings.Repeat("  ", comment.level)
	author := comment.author
	if author == "" {
		author = unknownAuthor
	} else if !strings.HasPrefix(author, "[") {
		author = "u/" + author
	}
	header := indent + "- **" + author + "**"
	if comment.score != "" {
		header += " · " + comment.score + " points"
	}
	if comment.created != "" {
		header += " · " + comment.created
	}
	if comment.status != "" {
		header += " · *" + comment.status + "*"
	}
	var out strings.Builder
	out.WriteString(header + "\n")
	bodyIndent := indent + "  "
	for index, block := range comment.blocks {
		if index > 0 {
			out.WriteString("\n")
		}
		out.WriteString(prefixLines(block, bodyIndent, "") + "\n")
	}
	return out.String()
}

// redditCount parses a stated count ("1,234" included).
func redditCount(raw string) (int, bool) {
	value, err := strconv.Atoi(strings.ReplaceAll(strings.TrimSpace(raw), ",", ""))
	if err != nil || value < 0 {
		return 0, false
	}
	return value, true
}

// redditDate keeps the calendar date of a Reddit ISO timestamp.
func redditDate(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) >= len("2006-01-02") {
		return raw[:len("2006-01-02")]
	}
	return raw
}

// redditBase resolves the thread's relative links and loader URLs.
func redditBase() *url.URL {
	return &url.URL{Scheme: schemeHTTPS, Host: "www.reddit.com"}
}

// redditHiddenCopy reports an element holding what a reader does not see in
// the thread: a hidden subtree, such as the fold-more block of a comment whose
// replies are in the page. The block's slot="more-comments-permalink" does not
// make it a copy — at the fold, the same slot holds the only link to the
// comment's replies.
func redditHiddenCopy(node *html.Node) bool {
	return hasClass(node, "hidden")
}

// redditContinueLink reports a visible link to the page of a reply chain past
// the page's depth: "Continue this thread" in a thread page or a loader's
// answer, an "N more replies" link classed more-comments-link in a continued
// page.
func redditContinueLink(node *html.Node, hidden bool) bool {
	return node.DataAtom == atom.A && !hidden &&
		(hasClass(node, "more-comments-link") ||
			strings.Contains(strings.ToLower(nodeText(node)), "continue this thread"))
}

// redditMoreComments reports a more-comments loader element.
func redditMoreComments(node *html.Node) bool {
	return isElement(node, "faceplate-partial") && strings.Contains(nodeAttr(node, "src"), "/more-comments/")
}

// redditLoaderKey identifies a loader: its URL and its cursor.
func redditLoaderKey(node *html.Node) string {
	key := nodeAttr(node, "src")
	if cursor := firstWithAttr(node, "name", "cursor", nil); cursor != nil {
		key += "#" + nodeAttr(cursor, "value")
	}
	return key
}

// redditLoaders names, in DOM order, every loader and visible "Continue this
// thread" link still in a thread page — the site's loaders for loaders.go. A
// link is followable only from inside the comment whose replies it leads to.
func redditLoaders(doc *html.Node, page *url.URL) []pageLoader {
	post := redditThreadPost(doc)
	if post == nil {
		return nil
	}
	base := redditBase()
	referer := page.String()
	if permalink := nodeAttr(post, "permalink"); permalink != "" {
		if parsed, err := url.Parse(permalink); err == nil {
			referer = base.ResolveReference(parsed).String()
		}
	}
	var loaders []pageLoader
	var walk func(node *html.Node, comment string, hidden bool)
	walk = func(node *html.Node, comment string, hidden bool) {
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if child.Type != html.ElementNode {
				continue
			}
			childHidden := hidden || redditHiddenCopy(child)
			switch {
			case isElement(child, "head"):
				continue
			case isElement(child, "shreddit-comment"):
				walk(child, nodeAttr(child, "thingid"), childHidden)
				continue
			case redditMoreComments(child):
				if loader, ok := redditMoreCommentsLoader(child, base, referer); ok {
					loaders = append(loaders, loader)
				}
				continue
			case redditContinueLink(child, childHidden):
				if loader, ok := redditContinueLoader(child, comment, base, referer); ok {
					loaders = append(loaders, loader)
				}
				continue
			}
			walk(child, comment, childHidden)
		}
	}
	walk(doc, "", false)
	return loaders
}

// redditMoreCommentsLoader is one more-comments loader: its form (the cursor)
// POSTed to its src, answered by a fragment of the tree grafted in its place.
func redditMoreCommentsLoader(node *html.Node, base *url.URL, referer string) (pageLoader, bool) {
	src, err := url.Parse(nodeAttr(node, "src"))
	if err != nil {
		return pageLoader{}, false
	}
	form := url.Values{}
	var inputs func(*html.Node)
	inputs = func(current *html.Node) {
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			if child.Type == html.ElementNode && child.DataAtom == atom.Input && nodeAttr(child, "name") != "" {
				form.Add(nodeAttr(child, "name"), nodeAttr(child, "value"))
			}
			inputs(child)
		}
	}
	inputs(node)
	label := `"View more comments" loader`
	if match := redditReplyCountRe.FindStringSubmatch(strings.ToLower(nodeText(node))); match != nil {
		label = fmt.Sprintf(`"%s more replies" loader`, match[1])
		if match[1] == "1" {
			label = `"1 more reply" loader`
		}
	}
	return pageLoader{
		key:    redditLoaderKey(node),
		label:  label,
		method: http.MethodPost,
		target: base.ResolveReference(src).String(),
		form:   form,
		headers: map[string]string{
			headerReferer:     referer,
			headerAccept:      redditPartialAccept,
			"Origin":          base.Scheme + "://" + base.Host,
			headerContentType: mediaTypeForm,
		},
		// Reddit answers each loader with its quota: x-ratelimit-remaining
		// "199.0", x-ratelimit-reset in seconds, 200 requests a window.
		quotaHeaders: true,
		quotaRate:    &redditQuota,
		graft: func(body []byte, contentType string) error {
			container := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
			nodes, err := html.ParseFragment(bytes.NewReader(body), container)
			if err != nil {
				return fmt.Errorf("parse the loader's answer: %w", err)
			}
			items := redditTreeItems(nodes)
			if len(items) == 0 && !strings.Contains(contentType, "partial") {
				// An empty fragment is a loader whose comments are gone; a
				// whole page with none is not an answer to it.
				return fmt.Errorf(
					"answered by a page (%s, titled %q) holding no comments",
					contentType,
					redditPageTitle(body),
				)
			}
			spliceInPlace(node, items)
			return nil
		},
		drop: func() { detach(node) },
	}, true
}

// redditContinueLoader is one "Continue this thread" link inside comment:
// its page holds the comment again with the replies this page did not reach,
// grafted in the link's place.
func redditContinueLoader(link *html.Node, comment string, base *url.URL, referer string) (pageLoader, bool) {
	href, err := url.Parse(nodeAttr(link, "href"))
	if err != nil || comment == "" || nodeAttr(link, "href") == "" {
		return pageLoader{}, false
	}
	target := base.ResolveReference(href).String()
	return pageLoader{
		key:     "continue " + target,
		label:   fmt.Sprintf(`"Continue this thread" link below %s`, comment),
		method:  http.MethodGet,
		target:  target,
		headers: map[string]string{headerReferer: referer},
		graft: func(body []byte, _ string) error {
			page, err := html.Parse(bytes.NewReader(body))
			if err != nil {
				return fmt.Errorf("parse the continued thread: %w", err)
			}
			root := redditCommentByID(page, comment)
			if root == nil {
				if tree := firstWithAttr(page, "thingid", comment, nil); isElement(tree, "shreddit-comment-tree") &&
					firstElement(tree, "shreddit-comment") == nil {
					// The comment's own tree, served empty: Reddit serves
					// none of the replies the link stood for.
					detach(link)
					return nil
				}
				return fmt.Errorf("the continued thread's page (titled %q) does not hold comment %s",
					pageTitle(page), comment)
			}
			var children []*html.Node
			for child := root.FirstChild; child != nil; child = child.NextSibling {
				children = append(children, child)
			}
			spliceInPlace(link, redditTreeItems(children))
			return nil
		},
		drop: func() { detach(link) },
	}, true
}

// redditTreeItems returns, in order, the comments and loaders under nodes that
// are not inside another comment: the top of a fragment of the tree.
func redditTreeItems(nodes []*html.Node) []*html.Node {
	var items []*html.Node
	var collect func(*html.Node)
	collect = func(node *html.Node) {
		if node.Type != html.ElementNode || isElement(node, "head") {
			return
		}
		if isElement(node, "shreddit-comment") || redditMoreComments(node) {
			items = append(items, node)
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			collect(child)
		}
	}
	for _, node := range nodes {
		collect(node)
	}
	return items
}

// redditCommentByID returns the <shreddit-comment> whose thingid is id.
func redditCommentByID(node *html.Node, id string) *html.Node {
	if isElement(node, "shreddit-comment") && nodeAttr(node, "thingid") == id {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := redditCommentByID(child, id); found != nil {
			return found
		}
	}
	return nil
}

// redditPageTitle is the <title> of an HTML answer, "" when it has none; an
// answer that cannot be parsed says so in its place.
func redditPageTitle(body []byte) string {
	page, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return "unparsable: " + err.Error()
	}
	return pageTitle(page)
}

// pageTitle is the text of the first <title> under node, "" when none.
func pageTitle(node *html.Node) string {
	if title := firstElement(node, "title"); title != nil {
		return nodeText(title)
	}
	return ""
}
