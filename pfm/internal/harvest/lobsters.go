package harvest

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// The Lobsters story extractor. A story page (lobste.rs/s/<id>, optionally
// followed by its title slug) is rendered on the server whole: the story in
// li.story (data-shortid <id>; its byline states "N comments"), its text in
// div.story_content, then ol#story_comments holding every comment as nested
// li.comments_subtree > div.comment (data-shortid) with its replies in the
// subtree's own ol.comments — the depth is the DOM nesting. Folding is a
// client-side checkbox: every folded comment is in the page. A comment's text
// is div.comment_text; a removed comment keeps its place, its text only a
// span.na ("[Comment removed by author]"). Each comment's byline carries the
// author's avatar image and its voters box a vote-score link to /login for a
// signed-out reader: page furniture, never rendered — only the comment's text
// is, so an image a comment itself holds stays. The site has no pagination
// for a story's comments, so the extractor needs no loaders; it reconciles the
// comments it rendered against the stated count, which leaves out the removed
// placeholders, so they are counted apart. A stated comment the page did not
// serve and a count not read each flag the artifact partial and are named.
// Lobsters also answers /s/<id>.json, but the page already holds every comment
// and the stated count, so the extractor reads the page it was given.

const lobstersHost = "lobste.rs"

// lobstersNoText is the state of a comment the page serves without text.
const lobstersNoText = "no text"

var lobstersStatedRe = regexp.MustCompile(`^(\d[\d,]*) comments?$`)

// isLobstersStory reports a story page address: /s/<id>[/<slug>].
func isLobstersStory(page *url.URL) bool {
	rest, ok := strings.CutPrefix(page.Path, "/s/")
	return ok && strings.Trim(rest, "/") != ""
}

// lobstersComment is one comment as rendered.
type lobstersComment struct {
	id     string
	level  int
	author string
	posted string
	score  string
	// state is "" for a comment with its text; otherwise the placeholder the
	// page shows in its place ("Comment removed by author").
	state  string
	blocks []string
}

// lobstersCommentTree reads the comments under list, in page order, each at the
// depth its subtree nests it.
func lobstersCommentTree(list *html.Node, level int, renderer markdownRenderer, into *[]lobstersComment) {
	for child := list.FirstChild; child != nil; child = child.NextSibling {
		if child.Type != html.ElementNode {
			continue
		}
		switch {
		case child.DataAtom == atom.Li && hasClass(child, "comments_subtree"):
			lobstersCommentTree(child, level, renderer, into)
		case child.DataAtom == atom.Ol && hasClass(child, "comments"):
			lobstersCommentTree(child, level+1, renderer, into)
		case child.DataAtom == atom.Div && hasClass(child, "comment") && nodeAttr(child, "data-shortid") != "":
			*into = append(*into, lobstersReadComment(child, level, renderer))
		}
	}
}

// lobstersByline reads a byline's author (the profile link that carries a
// name, not the avatar's) and time.
func lobstersByline(byline *html.Node) (author, posted string) {
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			switch {
			case node.DataAtom == atom.A && author == "" && strings.HasPrefix(nodeAttr(node, "href"), "/~"):
				author = nodeText(node)
			case node.DataAtom == atom.Time && posted == "":
				posted = lobstersDate(node)
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	if byline != nil {
		walk(byline)
	}
	return author, posted
}

// lobstersDate renders a time element's Unix instant (data-at-unix) as
// "2006-01-02 15:04 UTC"; its own text when the instant is unreadable.
func lobstersDate(node *html.Node) string {
	if unix, err := strconv.ParseInt(nodeAttr(node, "data-at-unix"), 10, 64); err == nil {
		return time.Unix(unix, 0).UTC().Format("2006-01-02 15:04 UTC")
	}
	return nodeText(node)
}

// lobstersScore reads the vote score off a voters box, as text; "" when none.
func lobstersScore(node *html.Node) string {
	if voters := firstClass(node, "voters"); voters != nil {
		if upvoter := firstClass(voters, "upvoter"); upvoter != nil {
			return nodeText(upvoter)
		}
	}
	return ""
}

// lobstersReadComment reads one div.comment at level.
func lobstersReadComment(node *html.Node, level int, renderer markdownRenderer) lobstersComment {
	comment := lobstersComment{id: nodeAttr(node, "data-shortid"), level: level, score: lobstersScore(node)}
	comment.author, comment.posted = lobstersByline(firstClass(node, "byline"))
	text := firstClass(node, "comment_text")
	if text == nil {
		comment.state = lobstersNoText
		return comment
	}
	if placeholder := firstClass(text, "na"); placeholder != nil && nodeText(placeholder) == nodeText(text) {
		comment.state = strings.Trim(nodeText(placeholder), "[] ")
		if comment.state == "" {
			comment.state = lobstersNoText
		}
		return comment
	}
	comment.blocks = renderer.blocks(text)
	return comment
}

// lobstersStated reads the story's stated comment count off its byline: "N
// comments", or "no comments". false when the byline states neither.
func lobstersStated(story *html.Node) (int, bool) {
	label := firstClass(story, "comments_label")
	if label == nil {
		return 0, false
	}
	text := strings.ToLower(nodeText(label))
	if strings.Contains(text, "no comments") {
		return 0, true
	}
	for _, field := range []string{text, strings.TrimSpace(strings.TrimPrefix(text, "|"))} {
		if match := lobstersStatedRe.FindStringSubmatch(field); match != nil {
			return redditCount(match[1])
		}
	}
	return 0, false
}

// extractLobstersStory renders a Lobsters story page: its header, the count
// line reconciling the comments loaded against the count the story states,
// its text, and every comment in thread order with its author, time, score
// and depth.
func extractLobstersStory(doc *html.Node, page *url.URL) (siteExtraction, bool) {
	story := firstClassElement(doc, atom.Li, "story")
	list := firstWithAttr(doc, "id", "story_comments", nil)
	if story == nil || list == nil || nodeAttr(story, "data-shortid") == "" {
		return siteExtraction{}, false
	}
	id := nodeAttr(story, "data-shortid")
	renderer := markdownRenderer{base: page}
	var comments []lobstersComment
	lobstersCommentTree(list, 0, renderer, &comments)
	loaded := 0
	placeholders := 0
	for _, comment := range comments {
		if comment.state == "" {
			loaded++
		} else {
			placeholders++
		}
	}

	var gaps []string
	stated, statedKnown := lobstersStated(story)
	statedText := "count not read (the story's byline states no comment count)"
	surplus := ""
	if statedKnown {
		statedText = strconv.Itoa(stated)
		switch rest := stated - loaded; {
		case rest > 0:
			gaps = append(gaps, fmt.Sprintf("%d stated comment(s) not in the page (removed since the count, "+
				"or not shown to a signed-out reader)", rest))
		case rest < 0:
			surplus = fmt.Sprintf(" · %d more loaded than stated", -rest)
		}
	} else {
		gaps = append(gaps, "the stated comment count was not read (the story's byline states none)")
	}
	countLine := fmt.Sprintf("**Comments:** %s stated · %d loaded", statedText, loaded)
	if placeholders > 0 {
		countLine += fmt.Sprintf(" (%d comment(s) removed, placeholders without text, not counted)", placeholders)
	}
	countLine += surplus
	if len(gaps) > 0 {
		countLine += " · gaps: " + strings.Join(gaps, "; ")
	}

	var out strings.Builder
	title, link := "", ""
	if heading := firstClass(story, "link"); heading != nil {
		if anchor := firstElement(heading, "a"); anchor != nil {
			title, link = nodeText(anchor), renderer.resolve(nodeAttr(anchor, "href"))
		}
	}
	if title == "" {
		title = pageTitle(doc)
	}
	out.WriteString("# " + title + "\n\n")
	var meta []string
	author, posted := lobstersByline(firstClass(story, "byline"))
	if author != "" {
		meta = append(meta, "**Author:** "+author)
	}
	if score := lobstersScore(story); score != "" {
		meta = append(meta, "**Score:** "+score)
	}
	if posted != "" {
		meta = append(meta, "**Posted:** "+posted)
	}
	if tags := firstClass(story, "tags"); tags != nil {
		meta = append(meta, "**Tags:** "+nodeText(tags))
	}
	if len(meta) > 0 {
		out.WriteString(strings.Join(meta, " · ") + "  \n")
	}
	threadURL := "https://" + lobstersHost + "/s/" + id
	out.WriteString("**Thread:** " + threadURL + "  \n")
	if link != "" && !strings.HasPrefix(link, threadURL) {
		out.WriteString("**Link:** " + link + "  \n")
	}
	out.WriteString(countLine + "\n\n")
	if text := firstClass(doc, "story_content"); text != nil {
		if blocks := renderer.blocks(text); len(blocks) > 0 {
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
		progress := fmt.Sprintf("%d comments loaded, the stated count not read", loaded)
		if statedKnown {
			progress = fmt.Sprintf("%d of %d comments loaded", loaded, stated)
		}
		partial = "lobsters story: " + progress + " — " + strings.Join(gaps, "; ")
	}
	return siteExtraction{markdown: out.String(), partial: partial}, true
}

func (comment lobstersComment) markdown() string {
	indent := strings.Repeat("  ", comment.level)
	author := comment.author
	if author == "" {
		author = unknownAuthor
	}
	header := indent + "- **" + author + "**"
	if comment.posted != "" {
		header += " · " + comment.posted
	}
	if comment.score != "" {
		header += " · " + comment.score + " points"
	}
	header += " · [#" + comment.id + "](https://" + lobstersHost + "/c/" + comment.id + ")"
	if comment.state != "" {
		header += " · *" + comment.state + "*"
	}
	var out strings.Builder
	out.WriteString(header + "\n")
	for index, block := range comment.blocks {
		if index > 0 {
			out.WriteString("\n")
		}
		out.WriteString(prefixLines(block, indent+"  ", "") + "\n")
	}
	return out.String()
}
