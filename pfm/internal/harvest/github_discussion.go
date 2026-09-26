package harvest

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// The GitHub discussion extractor. A discussion page
// (github.com/<owner>/<repo>/discussions/<n>) is rendered on the server but
// holds only part of its thread: its first and last comments, the middle
// folded behind a "N hidden items" pagination form, and of each comment only
// its last few replies, the earlier ones behind a "Show N previous replies"
// form. Its API (GraphQL) needs a token, so the extractor follows the page's
// own forms instead: each is a same-host GET whose answer is a fragment of
// the thread — the pagination form's answer holds the next comments (and a
// form for the rest), taking the form's place; a replies form's answer is the
// comment's whole reply list, replacing the list it names — through
// loaders.go's budget like any loader. It then renders the post and every
// comment in thread order, each reply under its comment, and reconciles what
// it loaded against the counts the page states: the discussion's comments and
// replies ("831 comments · 1,785 replies") apart, and each comment's replies
// ("6 replies"). A form still in the page, a count not read, and a stated
// item the page did not serve each flag the artifact partial and are named.

const (
	githubDiscussionSegment = "discussions"
	// githubCommentIDPrefix is the id of a comment's element; the element
	// holding a comment's replies carries githubRepliesIDPrefix before it.
	githubCommentIDPrefix = "discussioncomment-"
	githubRepliesIDPrefix = "child-comments-"
)

// githubDiscussionStatedRe reads the discussion's stated counts from its
// header byline: "Oct 24, 2022 · 831 comments · 1,785 replies".
var (
	githubDiscussionStatedRe = regexp.MustCompile(`(\d[\d,]*) comments? · (\d[\d,]*) repl(?:y|ies)\b`)
	githubRepliesStatedRe    = regexp.MustCompile(`^\s*(\d[\d,]*) repl(?:y|ies)\s*$`)
	githubThreadsPathRe      = regexp.MustCompile(`^/comments/(\d+)/threads$`)
)

// githubDiscussion is the discussion a page is, as its og:url names it.
type githubDiscussion struct {
	owner, repo string
	number      int
}

func (discussion githubDiscussion) path() string {
	return "/" + discussion.owner + "/" + discussion.repo + "/" + githubDiscussionSegment + "/" +
		strconv.Itoa(discussion.number)
}

func (discussion githubDiscussion) threadURL() string {
	return "https://" + githubHost + discussion.path()
}

// githubDiscussionPath reads a discussion's address:
// /<owner>/<repo>/discussions/<n>, a trailing slash allowed.
func githubDiscussionPath(path string) (githubDiscussion, bool) {
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(path, "/"), "/"), "/")
	if len(parts) != 4 || parts[0] == "" || parts[1] == "" || parts[2] != githubDiscussionSegment {
		return githubDiscussion{}, false
	}
	number, err := strconv.Atoi(parts[3])
	if err != nil || number <= 0 {
		return githubDiscussion{}, false
	}
	return githubDiscussion{owner: parts[0], repo: parts[1], number: number}, true
}

// isGitHubDiscussionAddress narrows the extractor's host claim to discussion
// addresses; every other GitHub page goes to the issue extractor.
func isGitHubDiscussionAddress(page *url.URL) bool {
	_, ok := githubDiscussionPath(page.Path)
	return ok
}

// githubDiscussionOf reads doc as a discussion's page: the requested address
// is one, and the page's og:url names the same discussion. false for any
// other page (a sign-in wall, a 404, a repository's discussion list).
func githubDiscussionOf(doc *html.Node, page *url.URL) (githubDiscussion, bool) {
	requested, ok := githubDiscussionPath(page.Path)
	if !ok {
		return githubDiscussion{}, false
	}
	canonical, ok := githubCanonicalPath(doc)
	if !ok {
		return githubDiscussion{}, false
	}
	named, ok := githubDiscussionPath(canonical)
	if !ok || named.number != requested.number {
		return githubDiscussion{}, false
	}
	return named, true
}

// githubDiscussionForm is one loader form in a discussion page.
type githubDiscussionForm struct {
	form   *html.Node
	target string
	// replies names, for a replies form, the element its answer replaces
	// and the comment it belongs to; "" for the pagination form.
	replies, comment string
	// button is the form's visible text: "801 hidden items",
	// "Show 1 previous reply".
	button string
}

// githubDiscussionForms lists the discussion's loader forms in doc, in DOM
// order: a GET form whose action is the discussion's own pagination or one of
// its comments' reply lists.
func githubDiscussionForms(doc *html.Node, page *url.URL, discussion githubDiscussion) []githubDiscussionForm {
	var forms []githubDiscussionForm
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.DataAtom == atom.Form {
			if found, ok := githubDiscussionFormOf(node, page, discussion); ok {
				forms = append(forms, found)
				return
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return forms
}

func githubDiscussionFormOf(
	form *html.Node,
	page *url.URL,
	discussion githubDiscussion,
) (githubDiscussionForm, bool) {
	if method := strings.ToLower(nodeAttr(form, "method")); method != "" && method != "get" {
		return githubDiscussionForm{}, false
	}
	action, err := url.Parse(strings.TrimSpace(nodeAttr(form, "action")))
	if err != nil || nodeAttr(form, "action") == "" {
		return githubDiscussionForm{}, false
	}
	target := page.ResolveReference(action)
	rest, under := strings.CutPrefix(target.Path, discussion.path())
	if !under || !strings.EqualFold(target.Hostname(), githubHost) {
		return githubDiscussionForm{}, false
	}
	found := githubDiscussionForm{form: form, button: strings.TrimSpace(nodeText(form))}
	switch match := githubThreadsPathRe.FindStringSubmatch(rest); {
	case rest == "/pages":
	case match != nil:
		found.comment = match[1]
		found.replies = nodeAttr(form, "data-replace-remote-form-target")
		if found.replies == "" {
			return githubDiscussionForm{}, false
		}
	default:
		return githubDiscussionForm{}, false
	}
	query := target.Query()
	for _, input := range elementsByTag(form, atom.Input) {
		if strings.EqualFold(nodeAttr(input, "type"), "hidden") && nodeAttr(input, "name") != "" {
			query.Set(nodeAttr(input, "name"), nodeAttr(input, "value"))
		}
	}
	target.RawQuery = query.Encode()
	target.Fragment = ""
	found.target = target.String()
	return found, true
}

// label names the form to a reader: "\"801 hidden items\" loader", "\"Show 1
// previous reply\" loader of comment #3963590".
func (found githubDiscussionForm) label() string {
	button := found.button
	if found.comment == "" {
		// The pagination form's text is its count and a "Load more…" button.
		button = strings.TrimSpace(strings.TrimSuffix(button, "Load more…"))
		return strconv.Quote(button) + " loader"
	}
	return strconv.Quote(button) + " loader of comment #" + found.comment
}

// githubDiscussionLoaders names the discussion's forms still in doc.
func githubDiscussionLoaders(doc *html.Node, page *url.URL) []pageLoader {
	discussion, ok := githubDiscussionOf(doc, page)
	if !ok {
		return nil
	}
	var loaders []pageLoader
	for _, found := range githubDiscussionForms(doc, page, discussion) {
		loaders = append(loaders, githubDiscussionLoader(doc, discussion, found))
	}
	return loaders
}

func githubDiscussionLoader(doc *html.Node, discussion githubDiscussion, found githubDiscussionForm) pageLoader {
	return pageLoader{
		key:     "github-discussion " + found.target,
		label:   found.label(),
		method:  http.MethodGet,
		target:  found.target,
		headers: map[string]string{headerAccept: "text/html"},
		graft: func(body []byte, contentType string) error {
			nodes, err := html.ParseFragment(bytes.NewReader(body), &html.Node{
				Type: html.ElementNode, Data: divTag, DataAtom: atom.Div,
			})
			if err != nil {
				return fmt.Errorf(
					"answered by a %s body that does not parse as HTML",
					discourseContentType(contentType),
				)
			}
			if err := githubDiscussionFragmentCheck(nodes, discussion, contentType); err != nil {
				return err
			}
			if found.comment == "" {
				spliceInPlace(found.form, nodes)
				return nil
			}
			list := firstWithAttr(doc, "id", found.replies, nil)
			if list == nil {
				return fmt.Errorf("answered, but comment #%s's reply list is not in this page", found.comment)
			}
			var replacement *html.Node
			for _, node := range nodes {
				if replacement = firstWithAttr(node, "id", found.replies, nil); replacement != nil {
					break
				}
			}
			if replacement == nil {
				return fmt.Errorf("answered by a %s body that is not comment #%s's reply list",
					discourseContentType(contentType), found.comment)
			}
			detach(replacement)
			spliceInPlace(list, []*html.Node{replacement})
			return nil
		},
		drop: func() { detach(found.form) },
	}
}

// githubDiscussionFragmentCheck proves a loader's answer a fragment of this
// discussion: it holds at least one comment, and every comment it names by
// address is this discussion's.
func githubDiscussionFragmentCheck(nodes []*html.Node, discussion githubDiscussion, contentType string) error {
	own := discussion.path() + "/comments/"
	comments := 0
	for _, node := range nodes {
		for _, comment := range githubCommentElements(node) {
			comments++
			if address := nodeAttr(comment, "data-url"); address != "" && !strings.HasPrefix(address, own) {
				return fmt.Errorf("answered by a fragment holding a comment of another discussion (%s)", address)
			}
		}
	}
	if comments == 0 {
		return fmt.Errorf("answered by a %s body holding no comment of this discussion",
			discourseContentType(contentType))
	}
	return nil
}

// githubCommentElements returns the comment elements under node (node
// included), in DOM order: the elements whose id is a comment's.
func githubCommentElements(node *html.Node) []*html.Node {
	var found []*html.Node
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.ElementNode {
			if id, ok := strings.CutPrefix(nodeAttr(current, "id"), githubCommentIDPrefix); ok && isDigits(id) {
				found = append(found, current)
			}
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return found
}

func isDigits(text string) bool {
	if text == "" {
		return false
	}
	for _, r := range text {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// elementsByTag returns the elements of one tag under node, in DOM order.
func elementsByTag(node *html.Node, tag atom.Atom) []*html.Node {
	var found []*html.Node
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.ElementNode && current.DataAtom == tag {
			found = append(found, current)
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return found
}
