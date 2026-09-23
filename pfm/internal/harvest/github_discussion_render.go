package harvest

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Rendering a GitHub discussion (github_discussion.go) from its page, once
// its loaders are followed: the post, then every comment in thread order with
// its replies under it, and the count line reconciling them.

// githubDiscussionEntry is one comment or reply as the page renders it.
type githubDiscussionEntry struct {
	id, author, at, body string
	hidden, answer       bool
	// stated is a comment's stated reply count; nil when the page states
	// none (a reply, a comment hidden by a maintainer).
	stated  *int
	replies []*githubDiscussionEntry
}

// githubInReplies reports the element holding a comment's replies: a
// comment's own fields are read without descending into it.
func githubInReplies(node *html.Node) bool {
	return strings.HasPrefix(nodeAttr(node, "id"), githubRepliesIDPrefix+githubCommentIDPrefix)
}

// githubFind returns the first element under node (node excluded) that match
// accepts, not descending into elements stop accepts.
func githubFind(node *html.Node, match, stop func(*html.Node) bool) *html.Node {
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type != html.ElementNode || stop(child) {
			continue
		}
		if match(child) {
			return child
		}
		if found := githubFind(child, match, stop); found != nil {
			return found
		}
	}
	return nil
}

// githubReadComment reads one comment element: its author, time, body, and
// whether a maintainer hid it or marked it the answer.
func githubReadComment(node *html.Node, page *url.URL) *githubDiscussionEntry {
	comment := &githubDiscussionEntry{author: unknownAuthor}
	classed := func(class string) func(*html.Node) bool {
		return func(candidate *html.Node) bool { return hasClass(candidate, class) }
	}
	if author := githubFind(node, classed("author"), githubInReplies); author != nil {
		comment.author = nodeText(author)
	} else if header := githubFind(node, classed("timeline-comment-header-text"), githubInReplies); header != nil {
		link := githubFind(header, func(candidate *html.Node) bool {
			href := nodeAttr(candidate, "href")
			return candidate.DataAtom == atom.A && href != "" && !strings.HasPrefix(href, "#")
		}, githubInReplies)
		if link != nil && nodeText(link) != "" {
			comment.author = nodeText(link)
		}
	}
	if at := githubFind(node, func(candidate *html.Node) bool {
		return candidate.Data == "relative-time"
	}, githubInReplies); at != nil {
		comment.at = nodeAttr(at, "datetime")
	}
	comment.hidden = githubFind(node, classed("unminimized-comment"), githubInReplies) == nil
	comment.answer = githubFind(node, classed("timeline-chosen-answer"), githubInReplies) != nil
	if body := githubFind(node, classed("js-comment-body"), githubInReplies); body != nil && !comment.hidden {
		comment.body = strings.Join(markdownRenderer{base: page}.blocks(body), "\n\n")
	}
	return comment
}

// githubStatedReplies reads a comment's "N replies" line from its timeline
// item, outside its replies and its body; nil when the page states none.
func githubStatedReplies(node *html.Node) *int {
	item := node.Parent
	for item != nil && !hasClass(item, "js-timeline-item") {
		item = item.Parent
	}
	if item == nil {
		return nil
	}
	var stated *int
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		for child := current.FirstChild; child != nil && stated == nil; child = child.NextSibling {
			switch {
			case child.Type == html.TextNode:
				if match := githubRepliesStatedRe.FindStringSubmatch(child.Data); match != nil {
					count := githubCount(match[1])
					stated = &count
				}
			case child.Type == html.ElementNode && !githubInReplies(child) && !hasClass(child, "js-comment-body"):
				walk(child)
			}
		}
	}
	walk(item)
	return stated
}

func githubCount(text string) int {
	count, _ := strconv.Atoi(strings.ReplaceAll(text, ",", ""))
	return count
}

// githubDiscussionStated reads the discussion's stated comment and reply
// counts from its header byline; ok false when the page states none.
func githubDiscussionStated(doc *html.Node) (comments, replies int, ok bool) {
	byline := githubFind(doc, func(candidate *html.Node) bool {
		for child := candidate.FirstChild; child != nil; child = child.NextSibling {
			if child.Type == html.ElementNode && child.Data == "relative-time" {
				return githubDiscussionStatedRe.MatchString(nodeText(candidate))
			}
		}
		return false
	}, func(*html.Node) bool { return false })
	if byline == nil {
		return 0, 0, false
	}
	match := githubDiscussionStatedRe.FindStringSubmatch(nodeText(byline))
	return githubCount(match[1]), githubCount(match[2]), true
}

// githubDiscussionThread reads the comments of doc in thread order, each
// once, its replies under it; orphans counts replies whose comment is not in
// the page.
func githubDiscussionThread(doc *html.Node, page *url.URL) (comments []*githubDiscussionEntry, orphans int) {
	byID := map[string]*githubDiscussionEntry{}
	seen := map[string]bool{}
	for _, node := range githubCommentElements(doc) {
		id := strings.TrimPrefix(nodeAttr(node, "id"), githubCommentIDPrefix)
		if seen[id] {
			continue
		}
		seen[id] = true
		parent := ""
		for ancestor := node.Parent; ancestor != nil; ancestor = ancestor.Parent {
			if githubInReplies(ancestor) {
				parent = strings.TrimPrefix(nodeAttr(ancestor, "id"), githubRepliesIDPrefix+githubCommentIDPrefix)
				break
			}
		}
		comment := githubReadComment(node, page)
		comment.id = id
		if parent == "" {
			comment.stated = githubStatedReplies(node)
			byID[id] = comment
			comments = append(comments, comment)
			continue
		}
		if owner := byID[parent]; owner != nil {
			owner.replies = append(owner.replies, comment)
			continue
		}
		orphans++
	}
	return comments, orphans
}

// githubDiscussionReconcile names the gap between a stated count and what
// was loaded; formsLeft is set when a loader still in the page already names
// where the rest is.
func githubDiscussionReconcile(noun string, stated, loaded int, formsLeft bool, gaps *[]string) string {
	rest := stated - loaded
	switch {
	case rest > 0 && !formsLeft:
		*gaps = append(*gaps, fmt.Sprintf("%d stated %s(s) not in the page (deleted or hidden since the count, "+
			"or not served by its loaders)", rest, noun))
	case rest < 0:
		return fmt.Sprintf("%d stated · %d loaded · %d more loaded than stated", stated, loaded, -rest)
	}
	return fmt.Sprintf("%d stated · %d loaded", stated, loaded)
}

func (discussion githubDiscussion) entryHeader(comment *githubDiscussionEntry) string {
	header := "**" + comment.author + "** · "
	if comment.at != "" {
		header += githubPosted(comment.at) + " · "
	}
	header += "[#" + comment.id + "](" + discussion.threadURL() + "#" + githubCommentIDPrefix + comment.id + ")"
	if comment.answer {
		header += " · *marked as answer*"
	}
	if comment.hidden {
		header += " · *hidden by a maintainer*"
	}
	return header
}

// extractGitHubDiscussion renders a GitHub discussion from its page.
func extractGitHubDiscussion(doc *html.Node, page *url.URL) (siteExtraction, bool) {
	discussion, ok := githubDiscussionOf(doc, page)
	if !ok {
		return siteExtraction{}, false
	}
	comments, orphans := githubDiscussionThread(doc, page)
	var gaps []string
	repliesLeft := map[string]bool{}
	pagesLeft := false
	for _, found := range githubDiscussionForms(doc, page, discussion) {
		gaps = append(gaps, found.label()+" not loaded")
		if found.comment == "" {
			pagesLeft = true
		} else {
			repliesLeft[found.comment] = true
		}
	}
	replies, hidden := 0, 0
	for _, comment := range comments {
		replies += len(comment.replies)
		if comment.hidden {
			hidden++
		}
		for _, reply := range comment.replies {
			if reply.hidden {
				hidden++
			}
		}
		if comment.stated != nil && len(comment.replies) < *comment.stated && !repliesLeft[comment.id] {
			gaps = append(gaps, fmt.Sprintf("comment #%s: %d replies stated, %d loaded", comment.id,
				*comment.stated, len(comment.replies)))
		}
	}
	if orphans > 0 {
		gaps = append(gaps, fmt.Sprintf("%d reply(s) whose comment is not in the page, not shown", orphans))
	}
	statedComments, statedReplies, stated := githubDiscussionStated(doc)
	var countLine string
	if stated {
		countLine = "**Comments:** " + githubDiscussionReconcile("comment", statedComments, len(comments),
			pagesLeft, &gaps) + " · **Replies:** " + githubDiscussionReconcile("reply", statedReplies, replies,
			pagesLeft || len(repliesLeft) > 0, &gaps)
	} else {
		gaps = append(gaps, "the stated comment and reply counts were not read")
		countLine = fmt.Sprintf("**Comments:** count not read · %d loaded · **Replies:** count not read · %d loaded",
			len(comments), replies)
	}
	if hidden > 0 {
		countLine += fmt.Sprintf(" (%d hidden by a maintainer, their text not shown)", hidden)
	}
	if len(gaps) > 0 {
		countLine += " · gaps: " + strings.Join(gaps, "; ")
	}

	var out strings.Builder
	title := pageTitle(doc)
	if node := firstClass(doc, "js-issue-title"); node != nil && nodeText(node) != "" {
		title = nodeText(node)
	}
	out.WriteString("# " + title + " (#" + strconv.Itoa(discussion.number) + ")\n\n")
	post := githubFind(doc, func(candidate *html.Node) bool {
		id, ok := strings.CutPrefix(nodeAttr(candidate, "id"), "discussion-")
		return ok && isDigits(id)
	}, githubInReplies)
	var opening *githubDiscussionEntry
	if post != nil {
		opening = githubReadComment(post, page)
		meta := "**Author:** " + opening.author
		if opening.at != "" {
			meta += " · **Opened:** " + githubPosted(opening.at)
		}
		out.WriteString(meta + "  \n")
	}
	out.WriteString("**Thread:** " + discussion.threadURL() + "  \n")
	out.WriteString(countLine + "\n\n")
	if opening != nil && opening.body != "" {
		out.WriteString(opening.body + "\n\n")
	}
	out.WriteString("---\n\n## Comments\n\n")
	if len(comments) == 0 {
		out.WriteString("*No comments are loaded.*\n")
	}
	for _, comment := range comments {
		out.WriteString("- " + discussion.entryHeader(comment) + "\n")
		if comment.body != "" {
			out.WriteString(prefixLines(comment.body, "  ", "") + "\n")
		}
		for _, reply := range comment.replies {
			out.WriteString("  - " + discussion.entryHeader(reply) + "\n")
			if reply.body != "" {
				out.WriteString(prefixLines(reply.body, "    ", "") + "\n")
			}
		}
	}
	partial := ""
	if len(gaps) > 0 {
		partial = fmt.Sprintf("github discussion: %d comments and %d replies loaded — %s", len(comments), replies,
			strings.Join(gaps, "; "))
	}
	return siteExtraction{markdown: out.String(), partial: partial}, true
}
