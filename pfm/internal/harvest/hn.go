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

// The Hacker News thread extractor. An item page (news.ycombinator.com/
// item?id=N) is rendered on the server whole: the story in table.fatitem (its
// tr.athing.submission, id N; the subline states "211 comments", or "discuss"
// for none), then table.comment-tree holding every comment as a flat row —
// tr.athing.comtr id=<comment id> — in tree order, its depth an indent
// attribute on td.ind (the spacer image's width is 40 px per level), not DOM
// nesting. A comment's text is div.commtext; a comment killed by flags keeps
// its row, as "[flagged]" with no text, only while it has replies to hold.
// Collapsing ("[3 more]") is client-side: every collapsed row is in the page.
// The stated count is the story's live comments (Firebase's descendants): it
// leaves out the dead placeholders, so they are counted apart. A thread too
// large for one page chains its pages by a "More" link (a.morelink rel=next,
// item?id=N&p=2); hnLoaders names it for Go to follow (loaders.go), and a
// page's answer is merged in by comment id — only once it proves itself the
// same story — its own "More" link replacing the one it was reached by. The
// extractor then reconciles the comments it rendered against the stated
// count: a "More" link left unfollowed, a stated comment the page did not
// serve, and a count not read each flag the artifact partial and are named.

const hnHost = "news.ycombinator.com"

// hnIndentWidth is the spacer width HN gives one level of nesting, the depth
// read when a row's td.ind carries no indent attribute.
const hnIndentWidth = 40

var hnStatedRe = regexp.MustCompile(`^(\d[\d,]*) comments?$`)

// hnThread is a Hacker News story's item page.
type hnThread struct {
	// id is the story's item id; story its tr.athing.submission, fatitem the
	// table holding it (the subline, the story's text).
	id      string
	story   *html.Node
	fatitem *html.Node
	// tree is the comment-tree table; nil on a page with no comments.
	tree *html.Node
	// rows are the comment rows in the page, in DOM (thread) order.
	rows []*html.Node
}

// hnThreadOf reads doc as a Hacker News story's item page; false for any
// other page (a listing, a comment's own page, a user page, a wall).
func hnThreadOf(doc *html.Node) (hnThread, bool) {
	fatitem := firstClassElement(doc, atom.Table, "fatitem")
	if fatitem == nil {
		return hnThread{}, false
	}
	story := firstClassElement(fatitem, atom.Tr, "submission")
	if story == nil || !hasClass(story, "athing") {
		return hnThread{}, false
	}
	id := nodeAttr(story, "id")
	if _, err := strconv.ParseUint(id, 10, 64); err != nil {
		return hnThread{}, false
	}
	thread := hnThread{id: id, story: story, fatitem: fatitem, tree: firstClassElement(doc, atom.Table, "comment-tree")}
	if thread.tree != nil {
		thread.rows = hnRows(thread.tree)
	}
	return thread, true
}

// firstClassElement is the first element under node (node included) of tag
// whose class list names class.
func firstClassElement(node *html.Node, tag atom.Atom, class string) *html.Node {
	if node.Type == html.ElementNode && node.DataAtom == tag && hasClass(node, class) {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := firstClassElement(child, tag, class); found != nil {
			return found
		}
	}
	return nil
}

// hnRows returns the comment rows (tr.athing.comtr) under tree in DOM order.
func hnRows(tree *html.Node) []*html.Node {
	var rows []*html.Node
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if child.Type != html.ElementNode {
				continue
			}
			if child.DataAtom == atom.Tr && hasClass(child, "comtr") {
				rows = append(rows, child)
				continue
			}
			walk(child)
		}
	}
	walk(tree)
	return rows
}

// hnMoreLinks returns the "More" links of doc that lead to another page of
// the thread (item?id=<id>&p=N), each with its target resolved against page.
func hnMoreLinks(doc *html.Node, page *url.URL, id string) ([]*html.Node, []*url.URL) {
	var nodes []*html.Node
	var targets []*url.URL
	for _, node := range relElements(doc, relNext) {
		if node.DataAtom != atom.A || !hasClass(node, "morelink") {
			continue
		}
		parsed, err := url.Parse(strings.TrimSpace(nodeAttr(node, "href")))
		if err != nil {
			continue
		}
		target := page.ResolveReference(parsed)
		target.Fragment = ""
		if target.Path != "/item" || target.Query().Get("id") != id {
			continue
		}
		nodes = append(nodes, node)
		targets = append(targets, target)
	}
	return nodes, targets
}

// hnPageLabel names a thread page to a reader: "page 2".
func hnPageLabel(target *url.URL) string {
	if number := target.Query().Get("p"); number != "" {
		return "page " + number
	}
	return "page 1"
}

// hnLoaders names the loaders still in a thread page, for loaders.go: every
// "More" link to a later page of the thread.
func hnLoaders(doc *html.Node, page *url.URL) []pageLoader {
	thread, ok := hnThreadOf(doc)
	if !ok {
		return nil
	}
	_, targets := hnMoreLinks(doc, page, thread.id)
	loaders := make([]pageLoader, 0, len(targets))
	for _, target := range targets {
		loaders = append(loaders, hnPageLoader(doc, page, thread, target))
	}
	return loaders
}

// hnPageLoader fetches one later page of the thread and merges its comment
// rows in by id; the answer's own "More" link replaces the one it was reached
// by, or the link goes when the answer has none.
func hnPageLoader(doc *html.Node, page *url.URL, thread hnThread, target *url.URL) pageLoader {
	matching := func() []*html.Node {
		var found []*html.Node
		nodes, targets := hnMoreLinks(doc, page, thread.id)
		for index, node := range nodes {
			if targets[index].String() == target.String() {
				found = append(found, node)
			}
		}
		return found
	}
	return pageLoader{
		key:     "hn-page " + target.String(),
		label:   "\"More\" link (" + hnPageLabel(target) + ")",
		method:  http.MethodGet,
		target:  target.String(),
		headers: map[string]string{headerReferer: page.String()},
		graft: func(body []byte, contentType string) error {
			answer, err := html.Parse(bytes.NewReader(body))
			if err != nil {
				return fmt.Errorf("answered by a %s body that does not parse as HTML",
					discourseContentType(contentType))
			}
			answered, ok := hnThreadOf(answer)
			switch {
			case !ok:
				return fmt.Errorf("answered by a page (%s, titled %q) that is not a Hacker News item page",
					discourseContentType(contentType), pageTitle(answer))
			case answered.id != thread.id:
				return fmt.Errorf("answered by the page of another item (titled %q)", pageTitle(answer))
			}
			if hnMerge(thread, answered.rows) == 0 {
				return fmt.Errorf("answered by a page (titled %q) holding no comment not already loaded",
					pageTitle(answer))
			}
			_, next := hnMoreLinks(answer, target, thread.id)
			for _, node := range matching() {
				if len(next) > 0 {
					hnSetAttr(node, "href", next[0].String())
					continue
				}
				detach(node)
			}
			return nil
		},
		drop: func() {
			for _, node := range matching() {
				detach(node)
			}
		},
	}
}

// hnMerge appends every row not already in thread's tree after its last row,
// and returns how many it appended.
func hnMerge(thread hnThread, incoming []*html.Node) int {
	if thread.tree == nil {
		return 0
	}
	rows := hnRows(thread.tree)
	if len(rows) == 0 {
		return 0
	}
	have := map[string]bool{}
	for _, row := range rows {
		have[nodeAttr(row, "id")] = true
	}
	last := rows[len(rows)-1]
	added := 0
	for _, row := range incoming {
		id := nodeAttr(row, "id")
		if id == "" || have[id] {
			continue
		}
		have[id] = true
		detach(row)
		last.Parent.InsertBefore(row, last.NextSibling)
		last = row
		added++
	}
	return added
}

// hnSetAttr sets node's attribute key to value.
func hnSetAttr(node *html.Node, key, value string) {
	for index := range node.Attr {
		if node.Attr[index].Key == key {
			node.Attr[index].Val = value
			return
		}
	}
	node.Attr = append(node.Attr, html.Attribute{Key: key, Val: value})
}

// hnComment is one comment row as rendered.
type hnComment struct {
	id     string
	level  int
	author string
	posted string
	// state is "" for a comment with its text; otherwise the placeholder the
	// row shows in its place ("flagged", "dead", "deleted").
	state  string
	blocks []string
}

// hnReadComment reads one comment row.
func hnReadComment(row *html.Node, renderer markdownRenderer) hnComment {
	comment := hnComment{id: nodeAttr(row, "id")}
	if indent := firstClassElement(row, atom.Td, "ind"); indent != nil {
		if level, err := strconv.Atoi(nodeAttr(indent, "indent")); err == nil {
			comment.level = level
		} else if spacer := firstElement(indent, "img"); spacer != nil {
			if width, err := strconv.Atoi(nodeAttr(spacer, "width")); err == nil {
				comment.level = width / hnIndentWidth
			}
		}
	}
	if user := firstClass(row, "hnuser"); user != nil {
		comment.author = nodeText(user)
	}
	if age := firstClass(row, "age"); age != nil {
		comment.posted = hnDate(nodeAttr(age, "title"))
		if comment.posted == "" {
			comment.posted = nodeText(age)
		}
	}
	if text := firstClass(row, "commtext"); text != nil {
		comment.blocks = renderer.blocks(text)
		return comment
	}
	// No text: the row is a placeholder, its own words ("[flagged]") the state.
	comment.state = "no text"
	if box := firstClass(row, "comment"); box != nil {
		var own []string
		for child := box.FirstChild; child != nil; child = child.NextSibling {
			if child.Type == html.TextNode {
				own = append(own, child.Data)
			}
		}
		if word := strings.Trim(strings.Join(strings.Fields(strings.Join(own, " ")), " "), "[] "); word != "" {
			comment.state = word
		}
	}
	return comment
}

// hnDate renders an age's title ("2026-09-22T21:43:44", UTC, optionally
// followed by a Unix time) as "2006-01-02 15:04 UTC"; "" when unreadable.
func hnDate(raw string) string {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return ""
	}
	parsed, err := time.Parse("2006-01-02T15:04:05", fields[0])
	if err != nil {
		return ""
	}
	return parsed.UTC().Format("2006-01-02 15:04 UTC")
}

// hnStated reads the story's stated comment count off its subline: "N
// comments", or "discuss" for none. false when the subline states neither.
func hnStated(thread hnThread) (int, bool) {
	var stated int
	var found bool
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if found {
			return
		}
		if node.Type == html.ElementNode && node.DataAtom == atom.A {
			text := strings.Join(strings.Fields(nodeText(node)), " ")
			if match := hnStatedRe.FindStringSubmatch(text); match != nil {
				stated, found = redditCount(match[1])
				if found {
					return
				}
			}
			if text == "discuss" {
				stated, found = 0, true
				return
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	if subline := firstClass(thread.fatitem, "subtext"); subline != nil {
		walk(subline)
	}
	return stated, found
}

// extractHNThread renders a Hacker News story's item page: its header, the
// count line reconciling the comments loaded against the count the story
// states, its text, and every comment in thread order with its author, time
// and depth.
func extractHNThread(doc *html.Node, page *url.URL) (siteExtraction, bool) {
	thread, ok := hnThreadOf(doc)
	if !ok {
		return siteExtraction{}, false
	}
	renderer := markdownRenderer{base: page}
	comments := make([]hnComment, 0, len(thread.rows))
	loaded := 0
	placeholders := map[string]int{}
	var states []string
	for _, row := range thread.rows {
		comment := hnReadComment(row, renderer)
		comments = append(comments, comment)
		if comment.state == "" {
			loaded++
			continue
		}
		if placeholders[comment.state] == 0 {
			states = append(states, comment.state)
		}
		placeholders[comment.state]++
	}

	var gaps []string
	_, more := hnMoreLinks(doc, page, thread.id)
	for _, target := range more {
		gaps = append(gaps, fmt.Sprintf("the comments on %s are not loaded (\"More\" link)", hnPageLabel(target)))
	}
	stated, statedKnown := hnStated(thread)
	statedText := "count not read (the story's subline states no comment count)"
	surplus := ""
	if statedKnown {
		statedText = strconv.Itoa(stated)
		rest := stated - loaded
		switch {
		case rest > 0 && len(gaps) == 0:
			gaps = append(gaps, fmt.Sprintf(
				"%d stated comment(s) not in the page (killed or deleted since the count, "+
					"or not shown to a signed-out reader)", rest))
		case rest > 0:
			gaps = append(gaps, fmt.Sprintf("%d stated comment(s) not loaded", rest))
		case rest < 0:
			surplus = fmt.Sprintf(" · %d more loaded than stated", -rest)
		}
	} else {
		gaps = append(gaps, "the stated comment count was not read (the story's subline states none)")
	}
	countLine := fmt.Sprintf("**Comments:** %s stated · %d loaded", statedText, loaded)
	if len(states) > 0 {
		var parts []string
		for _, state := range states {
			parts = append(parts, fmt.Sprintf("%d %s", placeholders[state], state))
		}
		countLine += " (" + strings.Join(parts, ", ") + " placeholder(s) without text, not counted)"
	}
	countLine += surplus
	if len(gaps) > 0 {
		countLine += " · gaps: " + strings.Join(gaps, "; ")
	}

	var out strings.Builder
	title := ""
	if line := firstClass(thread.story, "titleline"); line != nil {
		if link := firstElement(line, "a"); link != nil {
			title = nodeText(link)
		}
	}
	if title == "" {
		title = pageTitle(doc)
	}
	out.WriteString("# " + title + "\n\n")
	var meta []string
	if subline := firstClass(thread.fatitem, "subtext"); subline != nil {
		if author := firstClass(subline, "hnuser"); author != nil {
			meta = append(meta, "**Author:** "+nodeText(author))
		}
		if score := firstClass(subline, "score"); score != nil {
			meta = append(meta, "**Score:** "+nodeText(score))
		}
		if age := firstClass(subline, "age"); age != nil {
			if posted := hnDate(nodeAttr(age, "title")); posted != "" {
				meta = append(meta, "**Posted:** "+posted)
			}
		}
	}
	if len(meta) > 0 {
		out.WriteString(strings.Join(meta, " · ") + "  \n")
	}
	threadURL := "https://" + hnHost + "/item?id=" + thread.id
	out.WriteString("**Thread:** " + threadURL + "  \n")
	if line := firstClass(thread.story, "titleline"); line != nil {
		if link := firstElement(line, "a"); link != nil {
			if href := renderer.resolve(nodeAttr(link, "href")); href != "" && href != threadURL {
				out.WriteString("**Link:** " + href + "  \n")
			}
		}
	}
	out.WriteString(countLine + "\n\n")
	if text := firstClass(thread.fatitem, "toptext"); text != nil {
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
		partial = "hacker news thread: " + progress + " — " + strings.Join(gaps, "; ")
	}
	// Every comment HN serves a signed-out reader is in the server's page; a
	// browser render shows no more.
	return siteExtraction{markdown: out.String(), partial: partial}, true
}

func (comment hnComment) markdown() string {
	indent := strings.Repeat("  ", comment.level)
	author := comment.author
	if author == "" {
		author = unknownAuthor
	}
	header := indent + "- **" + author + "**"
	if comment.posted != "" {
		header += " · " + comment.posted
	}
	header += " · [#" + comment.id + "](https://" + hnHost + "/item?id=" + comment.id + ")"
	if comment.state != "" {
		header += " · *" + comment.state + "*"
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
