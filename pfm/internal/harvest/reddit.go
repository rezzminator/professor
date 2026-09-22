package harvest

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// The Reddit thread extractor. A thread page (server-rendered or hydrated in
// the browser rung) carries the post as <shreddit-post> and every comment as a
// nested <shreddit-comment> whose attributes hold its author, score, depth and
// id, and whose slot="comment" child holds its body. Branches the page did not
// load sit behind <faceplate-partial src=".../more-comments/..."> loaders
// ("N more replies", "View more comments"), and a reply chain past the page's
// depth limit behind a visible "Continue this thread" link to a separate page
// (every comment also carries a hidden copy of that link for its folded
// state, which is not a gap). The extractor renders the post and
// the tree in DOM order and reconciles the comments it rendered against the
// count the thread states, naming every gap — deleted and removed stubs,
// unexpanded loaders, and comments that are not in the page at all.

var redditReplyCountRe = regexp.MustCompile(`(\d[\d,]*)\s+more\s+repl`)

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
	base := &url.URL{Scheme: schemeHTTPS, Host: "www.reddit.com"}
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
			childHidden := hidden || hasClass(child, "hidden")
			switch {
			case isElement(child, "head"):
				continue
			case child.DataAtom == atom.A && !childHidden &&
				strings.Contains(strings.ToLower(nodeText(child)), "continue this thread"):
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
			case isElement(child, "faceplate-partial") && strings.Contains(nodeAttr(child, "src"), "/more-comments/"):
				key := nodeAttr(child, "src")
				if cursor := firstWithAttr(child, "name", "cursor", nil); cursor != nil {
					key += "#" + nodeAttr(cursor, "value")
				}
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
	notInPage := 0
	if statedKnown {
		notInPage = stated - len(comments) - hiddenReplies
	}
	if notInPage > 0 {
		gaps = append(gaps, fmt.Sprintf(
			"%d not in the page (behind a loader or link of unknown size, or deleted/removed without a stub)",
			notInPage,
		))
	}

	statedText := "unstated"
	if statedKnown {
		statedText = strconv.Itoa(stated)
	}
	countLine := fmt.Sprintf(
		"**Comments:** %s stated · %d in this page (%d deleted, %d removed)",
		statedText,
		len(comments),
		deleted,
		removed,
	)
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

	partial := ""
	if len(gaps) > 0 {
		partial = fmt.Sprintf(
			"reddit thread: %d of %s comments in the page — %s",
			len(comments),
			statedText,
			strings.Join(gaps, "; "),
		)
	}
	return siteExtraction{
		markdown:          out.String(),
		partial:           partial,
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
		author = "[unknown]"
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
