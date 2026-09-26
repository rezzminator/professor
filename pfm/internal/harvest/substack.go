package harvest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// The Substack post extractor. A post page (on <name>.substack.com or a
// publication's own domain, so it is known by its markup: the substackcdn.com
// assets and the window._preloads script) renders its body but none of its
// comments; the comments load after the page from the publication's API. The
// page's preload names the post — its id, its body and its stated
// comment_count — and the extractor reads the whole comment tree from
// /api/v1/post/{id}/comments?all_comments=true on the page's own host,
// unauthenticated, through loaders.go's budget: one answer, checked to be
// this post's (every comment names its post) before it is kept in the page as
// a harvester-substack-answer element, so a later conversion replays it like
// any followed loader. The comments render as a tree (social_thread.go). The
// API serves a comment deleted, blocked or removed by a moderator as a mark
// with no body, which its replies nest under; comment_count leaves those out.

const (
	substackAnswerTag = "harvester-substack-answer"
	substackCDNHost   = "substackcdn.com"
	substackPreloads  = "window._preloads"
)

type substackByline struct {
	Name   string `json:"name"`
	Handle string `json:"handle"`
}

type substackPost struct {
	ID           int              `json:"id"`
	Slug         string           `json:"slug"`
	Title        string           `json:"title"`
	PostDate     string           `json:"post_date"`
	Audience     string           `json:"audience"`
	CanonicalURL string           `json:"canonical_url"`
	BodyHTML     string           `json:"body_html"`
	CommentCount *int             `json:"comment_count"`
	Bylines      []substackByline `json:"publishedBylines"`
}

type substackComment struct {
	ID       int               `json:"id"`
	PostID   int               `json:"post_id"`
	Body     *string           `json:"body"`
	Date     string            `json:"date"`
	Status   string            `json:"status"`
	Deleted  bool              `json:"deleted"`
	Name     string            `json:"name"`
	Handle   string            `json:"handle"`
	Children []substackComment `json:"children"`
}

type substackComments struct {
	Comments []substackComment `json:"comments"`
	Hidden   []json.RawMessage `json:"automod_hidden_comments"`
}

// substackPage is what a post page holds: its preloaded post and the API
// answer kept for it.
type substackPage struct {
	post     substackPost
	comments substackComments
	read     bool
	dropped  bool
}

// substackPreload decodes the page's window._preloads JSON; false when the
// page holds none.
func substackPreload(doc *html.Node) (map[string]json.RawMessage, bool) {
	var literal string
	var walk func(*html.Node) bool
	walk = func(node *html.Node) bool {
		if node.Type == html.ElementNode && node.DataAtom == atom.Script {
			text := rawText(node)
			if _, rest, found := strings.Cut(text, substackPreloads); found {
				if _, rest, found = strings.Cut(rest, "JSON.parse("); found {
					if err := json.NewDecoder(strings.NewReader(rest)).Decode(&literal); err != nil {
						obs.Logger(context.Background()).Warn("harvest: a Substack page's preload is not a JSON string",
							obs.FieldErr, err.Error())
					}
					return true
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if walk(child) {
				return true
			}
		}
		return false
	}
	if !walk(doc) || literal == "" {
		return nil, false
	}
	var preload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(literal), &preload); err != nil {
		obs.Logger(context.Background()).Warn("harvest: a Substack page's preload does not decode",
			obs.FieldErr, err.Error())
		return nil, false
	}
	return preload, true
}

// isSubstack knows a Substack page by its markup: an asset from substackcdn.com
// and the window._preloads script.
func isSubstack(doc *html.Node) bool {
	cdn, preloads := false, false
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if cdn && preloads {
			return
		}
		if node.Type == html.ElementNode {
			switch node.DataAtom {
			case atom.Link, atom.Script:
				for _, key := range []string{"href", "src"} {
					if parsed, err := url.Parse(nodeAttr(node, key)); err == nil &&
						strings.EqualFold(parsed.Hostname(), substackCDNHost) {
						cdn = true
					}
				}
				if node.DataAtom == atom.Script && strings.Contains(rawText(node), substackPreloads) {
					preloads = true
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return cdn && preloads
}

// substackPageOf reads the post a page preloads and the answer kept for it;
// false for any other page (a home page, an archive, a wall).
func substackPageOf(doc *html.Node) (substackPage, bool) {
	preload, ok := substackPreload(doc)
	if !ok || len(preload["post"]) == 0 {
		return substackPage{}, false
	}
	var state substackPage
	if err := json.Unmarshal(preload["post"], &state.post); err != nil {
		obs.Logger(context.Background()).Warn("harvest: a Substack page's preloaded post does not decode",
			obs.FieldErr, err.Error())
		return substackPage{}, false
	}
	if state.post.ID <= 0 || state.post.CommentCount == nil {
		return substackPage{}, false
	}
	for _, node := range keptAnswers(doc, substackAnswerTag) {
		if nodeAttr(node, "dropped") != "" {
			state.dropped = true
			continue
		}
		if err := json.Unmarshal([]byte(rawText(node)), &state.comments); err != nil {
			obs.Logger(context.Background()).Warn("harvest: a kept Substack answer no longer decodes; left out",
				obs.FieldErr, err.Error())
			continue
		}
		state.read = true
	}
	return state, true
}

// substackLoaders names the post's comment tree while the page lacks it.
func substackLoaders(doc *html.Node, page *url.URL) []pageLoader {
	state, ok := substackPageOf(doc)
	if !ok || state.read || state.dropped {
		return nil
	}
	target := (&url.URL{
		Scheme:   page.Scheme,
		Host:     page.Host,
		Path:     "/api/v1/post/" + strconv.Itoa(state.post.ID) + "/comments",
		RawQuery: "all_comments=true&sort=best_first",
	}).String()
	key := "substack-api " + target
	return []pageLoader{{
		key:     key,
		label:   "the post's comment tree",
		method:  http.MethodGet,
		target:  target,
		headers: map[string]string{headerAccept: mediaTypeJSON},
		graft: func(body []byte, contentType string) error {
			if err := checkSubstack(state.post.ID, body, contentType); err != nil {
				return err
			}
			keepAnswer(doc, substackAnswerTag, key, "comments", 0, body)
			return nil
		},
		drop: func() { keepAnswer(doc, substackAnswerTag, key, "comments", 0, nil) },
	}}
}

// checkSubstack proves an answer is post id's comment tree: every comment in
// it names that post.
func checkSubstack(id int, body []byte, contentType string) error {
	var answer struct {
		Comments *[]substackComment `json:"comments"`
	}
	if err := json.Unmarshal(body, &answer); err != nil || answer.Comments == nil {
		return fmt.Errorf("answered by a %s body that is not the API's comment tree of post %d",
			discourseContentType(contentType), id)
	}
	var walk func([]substackComment) error
	walk = func(comments []substackComment) error {
		for index := range comments {
			switch comment := &comments[index]; {
			case comment.ID <= 0:
				return fmt.Errorf("answered by a comment tree holding a comment with no id")
			case comment.PostID != id:
				return fmt.Errorf("answered by a comment of post %d, not %d", comment.PostID, id)
			}
			if err := walk(comments[index].Children); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(*answer.Comments)
}

// substackUnavailable names why a comment the API serves with no body is not
// readable.
func substackUnavailable(comment *substackComment) string {
	switch {
	case comment.Body != nil && !comment.Deleted:
		return ""
	case comment.Deleted || comment.Status == socialDeleted:
		return socialDeleted
	case comment.Status == "moderator_removed":
		return "removed by a moderator"
	case comment.Status == "":
		return "served with no body"
	}
	return strings.ReplaceAll(comment.Status, "_", " ")
}

// substackPosts flattens a comment tree to socialPosts, each after its parent.
func substackPosts(comments []substackComment, parent, postURL string, out []socialPost) []socialPost {
	for index := range comments {
		comment := &comments[index]
		id := strconv.Itoa(comment.ID)
		post := socialPost{
			id: id, parent: parent, posted: githubPosted(comment.Date), author: unknownAuthor,
			link: postURL + "/comment/" + id, unavailable: substackUnavailable(comment),
		}
		switch {
		case comment.Handle != "":
			post.author = "@" + comment.Handle
		case comment.Name != "":
			post.author = comment.Name
		}
		if comment.Body != nil {
			post.body = strings.TrimSpace(strings.ReplaceAll(*comment.Body, "\r\n", "\n"))
		}
		out = substackPosts(comment.Children, id, postURL, append(out, post))
	}
	return out
}

// extractSubstackPost renders a Substack post from its preload and its
// comments from the API answer kept in its page.
func extractSubstackPost(doc *html.Node, page *url.URL) (siteExtraction, bool) {
	state, ok := substackPageOf(doc)
	if !ok {
		return siteExtraction{}, false
	}
	post := state.post
	link := post.CanonicalURL
	if link == "" {
		link = (&url.URL{Scheme: page.Scheme, Host: page.Host, Path: "/p/" + post.Slug}).String()
	}
	thread := socialThread{
		kind:  "substack post",
		title: post.Title,
		post: socialPost{
			id: strconv.Itoa(post.ID), author: unknownAuthor, posted: githubPosted(post.PostDate),
			link: link, body: htmlMarkdown(post.BodyHTML), stated: *post.CommentCount,
		},
		repliesRead: state.read,
		notServed: "the publication's comment tree did not hold them (hidden from a reader not signed in, " +
			"or posted after the tree was read)",
		unavailableUnstated: true,
	}
	var authors []string
	for _, byline := range post.Bylines {
		if byline.Handle != "" {
			authors = append(authors, "@"+byline.Handle)
		} else if byline.Name != "" {
			authors = append(authors, byline.Name)
		}
	}
	if len(authors) > 0 {
		thread.post.author = strings.Join(authors, ", ")
	}
	if post.Audience != "" && post.Audience != "everyone" {
		thread.gaps = append(thread.gaps, "the post is for "+strings.ReplaceAll(post.Audience, "_", " ")+
			" subscribers: its body may stop at the paywall")
	}
	if state.read {
		thread.replies = substackPosts(state.comments.Comments, thread.post.id, link, nil)
		if hidden := len(state.comments.Hidden); hidden > 0 {
			thread.gaps = append(thread.gaps,
				fmt.Sprintf("%d comment(s) hidden by the publication's automatic moderation, not served", hidden))
		}
	}
	markdown, partial := thread.render()
	return siteExtraction{markdown: markdown, partial: partial, apiRecord: state.read}, true
}
