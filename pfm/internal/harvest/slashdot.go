package harvest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// The Slashdot story extractor. A story page (news.slashdot.org/story/…)
// renders the story and the first part of its discussion as a nested comment
// tree; comments under the reader's threshold are only placeholders, and the
// rest of the discussion (D2.more_comments_num) loads after the page from the
// same host: a POST to /ajax.pl, op=comments_fetch, naming the discussion and
// the comments the page already holds (its D2.d2_seen string, without which
// the site answers nothing). With fetch_all and the lowest threshold the answer
// holds every further comment's markup, keyed comment_<cid>, and its place in
// the tree (update_data.new_cids_order with new_cids_data's parent ids), and
// states the discussion's total (totalcommentcnt). The answer is a JavaScript
// object literal, not JSON — bare keys — so it is read by quoting its keys.
// It is checked to be this discussion's comment list (every fragment links
// under the discussion's sid) before it is kept in the page as a
// harvester-slashdot-answer element, so a later conversion replays it like any
// followed loader. The page's comments and the answer's render as one tree,
// each comment once, reconciled `N stated · L loaded` against the total.

const (
	slashdotHost      = "slashdot.org"
	slashdotAnswerTag = "harvester-slashdot-answer"
	// slashdotListed names a comment the answer lists without its markup.
	slashdotListed = "listed without its text"
)

var (
	slashdotStoryPath = regexp.MustCompile(`^/story/\d{2}/\d{2}/\d{2}/\d+(?:/|$)`)
	slashdotCall      = regexp.MustCompile(`D2\.(discussion_id|more_comments_num)\((\d+)\)`)
	slashdotSeen      = regexp.MustCompile(`D2\.d2_seen\('([0-9,]*)'\)`)
	slashdotCommentID = regexp.MustCompile(`^comment_(\d+)$`)
	slashdotTreeID    = regexp.MustCompile(`^tree_(\d+)$`)
	slashdotPosted    = regexp.MustCompile(`on (\w+ \w+ \d{1,2}, \d{4} @\d{1,2}:\d{2}[AP]M)`)
	slashdotByline    = regexp.MustCompile(`Posted\s+by\s+(\S+)`)
)

// slashdotAnswer is what the extractor reads of a comments_fetch answer.
type slashdotAnswer struct {
	HTML   map[string]string `json:"html"`
	Update struct {
		Total *int    `json:"totalcommentcnt"`
		Order []int64 `json:"new_cids_order"`
		Data  []struct {
			Pid int64 `json:"pid"`
		} `json:"new_cids_data"`
	} `json:"update_data"`
}

// slashdotPage is what a story page holds: its discussion, the comments the
// site has still to send, and the answer kept for them.
type slashdotPage struct {
	id      string
	seen    string
	more    int
	answer  *slashdotAnswer
	dropped bool
}

// isSlashdotStory accepts a Slashdot address naming a story.
func isSlashdotStory(page *url.URL) bool {
	return slashdotStoryPath.MatchString(page.Path)
}

// slashdotScripts is the text of every script in doc: the page's D2 state.
func slashdotScripts(doc *html.Node) string {
	var text strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.DataAtom == atom.Script {
			text.WriteString(rawText(node) + "\n")
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return text.String()
}

// slashdotPageOf reads the discussion a story page names and the answer kept
// for it; false for any other page (a listing, a wall).
func slashdotPageOf(doc *html.Node, page *url.URL) (slashdotPage, bool) {
	if !isSlashdotStory(page) {
		return slashdotPage{}, false
	}
	scripts := slashdotScripts(doc)
	var state slashdotPage
	for _, call := range slashdotCall.FindAllStringSubmatch(scripts, -1) {
		if call[1] == "discussion_id" {
			state.id = call[2]
			continue
		}
		more, err := strconv.Atoi(call[2])
		if err != nil {
			obs.Logger(context.Background()).Warn("harvest: a Slashdot page's further comment count is not a number",
				obs.FieldErr, err.Error())
		}
		state.more = more
	}
	if state.id == "" {
		return slashdotPage{}, false
	}
	if seen := slashdotSeen.FindStringSubmatch(scripts); seen != nil {
		state.seen = seen[1]
	}
	for _, node := range keptAnswers(doc, slashdotAnswerTag) {
		if nodeAttr(node, "dropped") != "" {
			state.dropped = true
			continue
		}
		answer, err := slashdotDecode([]byte(rawText(node)))
		if err != nil {
			obs.Logger(context.Background()).Warn("harvest: a kept Slashdot answer no longer decodes; left out",
				obs.FieldErr, err.Error())
			continue
		}
		state.answer = &answer
	}
	return state, true
}

// slashdotLoaders names the discussion's further comments while the page
// lacks them.
func slashdotLoaders(doc *html.Node, page *url.URL) []pageLoader {
	state, ok := slashdotPageOf(doc, page)
	if !ok || state.answer != nil || state.dropped || state.more == 0 {
		return nil
	}
	key := "slashdot-comments " + state.id
	form := url.Values{
		"op": {"comments_fetch"}, "discussion_id": {state.id}, "fetch_all": {"1"}, "d2_seen": {state.seen},
		"threshold": {"-1"}, "highlightthresh": {"-1"},
	}
	return []pageLoader{{
		key:    key,
		label:  fmt.Sprintf("the discussion's %d further comments", state.more),
		method: http.MethodPost,
		target: (&url.URL{Scheme: page.Scheme, Host: page.Host, Path: "/ajax.pl"}).String(),
		form:   form,
		headers: map[string]string{
			headerContentType: mediaTypeForm, headerReferer: page.String(),
			"X-Requested-With": "XMLHttpRequest",
		},
		graft: func(body []byte, contentType string) error {
			if err := checkSlashdot(body, contentType, state.id); err != nil {
				return err
			}
			keepAnswer(doc, slashdotAnswerTag, key, "comments", 0, body)
			return nil
		},
		drop: func() { keepAnswer(doc, slashdotAnswerTag, key, "comments", 0, nil) },
	}}
}

// slashdotDecode reads a comments_fetch answer: a JavaScript object literal
// whose keys are bare, made JSON by quoting them.
func slashdotDecode(body []byte) (slashdotAnswer, error) {
	var answer slashdotAnswer
	if err := json.Unmarshal(jsLiteralJSON(body), &answer); err != nil {
		return answer, fmt.Errorf("decode the comment list: %w", err)
	}
	return answer, nil
}

// jsLiteralJSON quotes the bare keys of a JavaScript object literal (a word
// followed by a colon outside a string), and unescapes \' inside its strings,
// which JSON does not know; the rest passes as it is.
func jsLiteralJSON(src []byte) []byte {
	word := func(c byte) bool {
		return c == '_' || c == '$' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
	}
	var out bytes.Buffer
	out.Grow(len(src) + len(src)/16)
	for index := 0; index < len(src); {
		switch c := src[index]; {
		case c == '"':
			end := index + 1
			out.WriteByte('"')
			for end < len(src) && src[end] != '"' {
				switch {
				case src[end] == '\\' && end+1 < len(src) && src[end+1] == '\'':
					out.WriteByte('\'')
					end += 2
				case src[end] == '\\' && end+1 < len(src):
					out.Write(src[end : end+2])
					end += 2
				default:
					out.WriteByte(src[end])
					end++
				}
			}
			out.WriteByte('"')
			index = end + 1
		case word(c):
			end := index
			for end < len(src) && word(src[end]) {
				end++
			}
			next := end
			for next < len(src) && strings.IndexByte(" \t\r\n", src[next]) >= 0 {
				next++
			}
			if next < len(src) && src[next] == ':' {
				out.WriteString(`"` + string(src[index:end]) + `"`)
			} else {
				out.Write(src[index:end])
			}
			index = end
		default:
			out.WriteByte(c)
			index++
		}
	}
	return out.Bytes()
}

// checkSlashdot proves an answer is the discussion's comment list: it decodes,
// states the discussion's total, names a parent for every comment it lists,
// and every comment's markup is one it lists, linked under the discussion.
func checkSlashdot(body []byte, contentType, id string) error {
	answer, err := slashdotDecode(body)
	if err != nil || answer.HTML == nil {
		return fmt.Errorf("answered by a %s body that is not the discussion's comment list",
			discourseContentType(contentType))
	}
	if answer.Update.Total == nil {
		return fmt.Errorf("answered by a comment list that states no total")
	}
	if len(answer.Update.Order) != len(answer.Update.Data) {
		return fmt.Errorf("answered by a comment list naming %d parents for %d comments",
			len(answer.Update.Data), len(answer.Update.Order))
	}
	listed := map[string]bool{}
	for _, cid := range answer.Update.Order {
		listed["comment_"+strconv.FormatInt(cid, 10)] = true
	}
	for key, fragment := range answer.HTML {
		if !listed[key] {
			return fmt.Errorf("answered by a comment list holding a comment it does not list")
		}
		if !strings.Contains(fragment, "sid="+id+"&") {
			return fmt.Errorf("answered by a comment list of another discussion")
		}
	}
	return nil
}

// slashdotText is an element's text, whitespace collapsed; "" when absent.
func slashdotText(node *html.Node, key, value string) string {
	if found := firstWithAttr(node, key, value, nil); found != nil {
		return strings.Join(strings.Fields(rawText(found)), " ")
	}
	return ""
}

// slashdotComment reads comment cid from node (its markup: the page's comment
// element, or an answer's fragment); false when node holds only a placeholder.
func slashdotComment(node *html.Node, cid, host, sid string, base *url.URL) (socialPost, bool) {
	top := firstWithAttr(node, "id", "comment_top_"+cid, nil)
	if top == nil {
		return socialPost{}, false
	}
	post := socialPost{
		id: cid, author: unknownAuthor, posted: unknownPosted,
		link: "https://" + host + "/comments.pl?sid=" + sid + "&cid=" + cid,
	}
	if by := firstWithAttr(top, "class", "by", nil); by != nil {
		name := strings.TrimSpace(strings.TrimPrefix(strings.Join(strings.Fields(rawText(by)), " "), "by "))
		if link := firstElement(by, "a"); link != nil {
			name = strings.Join(strings.Fields(rawText(link)), " ")
		}
		if name != "" {
			post.author = name
		}
	}
	details := slashdotText(top, "id", "comment_otherdetails_"+cid)
	if posted := slashdotPosted.FindStringSubmatch(details); posted != nil {
		post.posted = posted[1]
	}
	heading := "**" + slashdotText(top, "id", "comment_link_"+cid) + "**"
	if score := slashdotText(top, "id", "comment_score_"+cid); score != "" {
		heading += " " + score
	}
	post.body = heading
	if body := firstWithAttr(node, "id", "comment_body_"+cid, nil); body != nil {
		renderer := markdownRenderer{base: base}
		post.body += "\n\n" + strings.Join(renderer.blocks(body), "\n\n")
	}
	return post, true
}

// slashdotParent is the comment a page comment answers: the nearest enclosing
// tree_<cid> item other than its own; "" for a top-level comment.
func slashdotParent(node *html.Node, cid string) string {
	for up := node.Parent; up != nil; up = up.Parent {
		if up.Type != html.ElementNode || up.DataAtom != atom.Li {
			continue
		}
		if match := slashdotTreeID.FindStringSubmatch(nodeAttr(up, "id")); len(match) > 1 && match[1] != cid {
			return match[1]
		}
	}
	return ""
}

// slashdotPosts is every comment the page and its kept answer hold, each once,
// in comment-id order, a comment whose parent is not held set under the story.
// The second result counts the page's comments.
func slashdotPosts(doc *html.Node, state *slashdotPage, page *url.URL) ([]socialPost, int) {
	posts := map[string]socialPost{}
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			if match := slashdotCommentID.FindStringSubmatch(nodeAttr(node, "id")); match != nil {
				if post, ok := slashdotComment(node, match[1], page.Host, state.id, page); ok {
					post.parent = slashdotParent(node, match[1])
					posts[post.id] = post
				}
				return
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	onPage := len(posts)
	if state.answer != nil {
		for index, number := range state.answer.Update.Order {
			cid := strconv.FormatInt(number, 10)
			if _, held := posts[cid]; held {
				continue
			}
			post := socialPost{
				id: cid, author: unknownAuthor, unavailable: slashdotListed,
				link: "https://" + page.Host + "/comments.pl?sid=" + state.id + "&cid=" + cid,
			}
			if fragment, ok := state.answer.HTML["comment_"+cid]; ok {
				holder := &html.Node{Type: html.ElementNode, Data: divTag, DataAtom: atom.Div}
				nodes, err := html.ParseFragment(strings.NewReader(fragment), holder)
				if err != nil {
					obs.Logger(context.Background()).Warn("harvest: a Slashdot comment's markup could not be parsed",
						"cid", cid, obs.FieldErr, err.Error())
				}
				for _, node := range nodes {
					holder.AppendChild(node)
				}
				if read, ok := slashdotComment(holder, cid, page.Host, state.id, page); ok {
					post = read
				}
			}
			if parent := state.answer.Update.Data[index].Pid; parent != 0 {
				post.parent = strconv.FormatInt(parent, 10)
			}
			posts[cid] = post
		}
	}
	out := make([]socialPost, 0, len(posts))
	for _, post := range posts {
		if _, held := posts[post.parent]; !held {
			post.parent = state.id
		}
		out = append(out, post)
	}
	sort.Slice(out, func(left, right int) bool {
		a, errA := strconv.ParseInt(out[left].id, 10, 64)
		b, errB := strconv.ParseInt(out[right].id, 10, 64)
		if errA != nil || errB != nil {
			return out[left].id < out[right].id
		}
		return a < b
	})
	return out, onPage
}

// slashdotStated is the comment count the page states for its own story (its
// comment bubble); the page's comments and those still to load when it
// states none.
func slashdotStated(doc *html.Node, page *url.URL, onPage, more int) int {
	var stated *int
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if stated != nil {
			return
		}
		if node.Type == html.ElementNode && nodeAttr(node, "class") == "comment-bubble" {
			if link := firstElement(node, "a"); link != nil {
				target, err := page.Parse(nodeAttr(link, "href"))
				count, countErr := strconv.Atoi(strings.TrimSpace(rawText(link)))
				if err == nil && countErr == nil && target.Path == page.Path {
					stated = &count
				}
			}
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	if stated != nil {
		return *stated
	}
	return onPage + more
}

// extractSlashdotStory renders a story and its discussion from the page and
// the comment list kept in it.
func extractSlashdotStory(doc *html.Node, page *url.URL) (siteExtraction, bool) {
	state, ok := slashdotPageOf(doc, page)
	if !ok {
		return siteExtraction{}, false
	}
	replies, onPage := slashdotPosts(doc, &state, page)
	title := ""
	if node := firstElement(doc, "title"); node != nil {
		title = strings.TrimSuffix(strings.Join(strings.Fields(rawText(node)), " "), " - Slashdot")
	}
	thread := socialThread{
		kind:  "slashdot story",
		title: title,
		post: socialPost{
			id: state.id, author: unknownAuthor, posted: unknownPosted,
			link: "https://" + page.Host + page.Path,
		},
		replies:     replies,
		repliesRead: true,
	}
	switch {
	case state.answer != nil:
		thread.post.stated = *state.answer.Update.Total
		thread.notServed = "the discussion's comment list did not serve them"
	case state.dropped:
		thread.post.stated = slashdotStated(doc, page, onPage, state.more)
		thread.notServed = "the request for the discussion's further comments failed"
	default:
		thread.post.stated = slashdotStated(doc, page, onPage, state.more)
		thread.notServed = "the discussion's further comments were not requested"
	}
	if byline := firstWithAttr(doc, "class", "story-byline", nil); byline != nil {
		text := strings.Join(strings.Fields(rawText(byline)), " ")
		if author := slashdotByline.FindStringSubmatch(text); author != nil {
			thread.post.author = author[1]
		}
		if posted := firstElement(byline, "time"); posted != nil {
			thread.post.posted = strings.TrimPrefix(strings.Join(strings.Fields(rawText(posted)), " "), "on ")
		}
	}
	if article := firstElement(doc, "article"); article != nil {
		story := "text-" + strings.TrimPrefix(nodeAttr(article, "id"), "firehose-")
		if body := firstWithAttr(article, "id", story, nil); body != nil {
			renderer := markdownRenderer{base: page}
			thread.post.body = strings.Join(renderer.blocks(body), "\n\n")
		}
	}
	markdown, partial := thread.render()
	return siteExtraction{markdown: markdown, partial: partial}, true
}
