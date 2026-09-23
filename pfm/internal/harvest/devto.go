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

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// The dev.to article extractor. An article page (dev.to/<user>/<slug>)
// renders only its top comments; the page names its article in the body's
// data-article-id. The extractor reads the article from dev.to's public API
// instead, unauthenticated — no key is ever sent — through loaders.go's
// budget: the article's record (/api/articles/{id}: its Markdown body and its
// stated comments_count), then its whole comment tree
// (/api/comments?a_id={id}, one answer). The record is checked to be this
// article's before it is kept in the page as a harvester-devto-answer
// element, so a later conversion replays it like any followed loader; the
// tree names no article, so it is checked to be a comment tree. An article
// whose record never loaded is not claimed: the page goes the generic path,
// the gap named in its partial marker. The comments render as a tree
// (social_thread.go); a comment its author deleted stays in the tree as a
// mark its replies nest under, and the API's count leaves it out.

const (
	devtoHost         = "dev.to"
	devtoAnswerTag    = "harvester-devto-answer"
	devtoKindArticle  = "article"
	devtoKindComments = "comments"
	devtoDeletedBody  = "[deleted]"
)

type devtoUser struct {
	Name     string `json:"name"`
	Username string `json:"username"`
}

type devtoArticle struct {
	ID            int        `json:"id"`
	Title         string     `json:"title"`
	URL           string     `json:"url"`
	PublishedAt   string     `json:"published_at"`
	BodyMarkdown  string     `json:"body_markdown"`
	CommentsCount *int       `json:"comments_count"`
	User          *devtoUser `json:"user"`
}

type devtoComment struct {
	IDCode    string         `json:"id_code"`
	CreatedAt string         `json:"created_at"`
	BodyHTML  string         `json:"body_html"`
	User      *devtoUser     `json:"user"`
	Children  []devtoComment `json:"children"`
}

// devtoPage is what an article page holds of its API answers.
type devtoPage struct {
	id       int
	article  *devtoArticle
	comments []devtoComment
	read     bool
	dropped  map[string]bool
}

// devtoArticleID reads the article id an article page names; false for any
// other page (a profile, a listing, a wall).
func devtoArticleID(doc *html.Node, page *url.URL) (int, bool) {
	if len(strings.Split(strings.Trim(page.Path, "/"), "/")) != 2 {
		return 0, false
	}
	var found string
	var walk func(*html.Node) bool
	walk = func(node *html.Node) bool {
		if node.Type == html.ElementNode {
			for _, attr := range node.Attr {
				if attr.Key == "data-article-id" {
					found = attr.Val
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
	walk(doc)
	id, err := strconv.Atoi(strings.TrimSpace(found))
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func devtoPageOf(doc *html.Node, page *url.URL) (devtoPage, bool) {
	id, ok := devtoArticleID(doc, page)
	if !ok {
		return devtoPage{}, false
	}
	state := devtoPage{id: id, dropped: map[string]bool{}}
	for _, node := range keptAnswers(doc, devtoAnswerTag) {
		if nodeAttr(node, "dropped") != "" {
			state.dropped[nodeAttr(node, "key")] = true
			continue
		}
		var err error
		switch kind := nodeAttr(node, "kind"); kind {
		case devtoKindArticle:
			var article devtoArticle
			if err = json.Unmarshal([]byte(rawText(node)), &article); err == nil {
				state.article = &article
			}
		case devtoKindComments:
			if err = json.Unmarshal([]byte(rawText(node)), &state.comments); err == nil {
				state.read = true
			}
		}
		if err != nil {
			obs.Logger(context.Background()).Warn("harvest: a kept dev.to answer no longer decodes; left out",
				"kind", nodeAttr(node, "kind"), obs.FieldErr, err.Error())
		}
	}
	return state, true
}

func devtoKey(target string) string { return "devto-api " + target }

// devtoLoaders names the API answer the article page still lacks: its
// record, then its comment tree.
func devtoLoaders(doc *html.Node, page *url.URL) []pageLoader {
	state, ok := devtoPageOf(doc, page)
	if !ok {
		return nil
	}
	id := strconv.Itoa(state.id)
	kind, target, label := devtoKindArticle, "https://"+devtoHost+"/api/articles/"+id, "the article's API record"
	switch {
	case state.article == nil:
	case !state.read:
		kind, target, label = devtoKindComments, "https://"+devtoHost+"/api/comments?a_id="+id,
			"the article's comment tree"
	default:
		return nil
	}
	key := devtoKey(target)
	if state.dropped[key] || (kind == devtoKindComments && state.dropped[devtoKey(
		"https://"+devtoHost+"/api/articles/"+id)]) {
		return nil
	}
	return []pageLoader{{
		key:     key,
		label:   label,
		method:  http.MethodGet,
		target:  target,
		headers: map[string]string{headerAccept: mediaTypeJSON},
		graft: func(body []byte, contentType string) error {
			if err := checkDevto(kind, state.id, body, contentType); err != nil {
				return err
			}
			keepAnswer(doc, devtoAnswerTag, key, kind, 0, body)
			return nil
		},
		drop: func() { keepAnswer(doc, devtoAnswerTag, key, kind, 0, nil) },
	}}
}

// checkDevto proves an answer is article id's record, or a comment tree.
func checkDevto(kind string, id int, body []byte, contentType string) error {
	notJSON := fmt.Errorf("answered by a %s body that is not the API's JSON for article %d's %s",
		discourseContentType(contentType), id, kind)
	if kind == devtoKindArticle {
		var article devtoArticle
		switch err := json.Unmarshal(body, &article); {
		case err != nil || article.ID == 0:
			return notJSON
		case article.ID != id:
			return fmt.Errorf("answered by the record of article %d, not %d", article.ID, id)
		case article.CommentsCount == nil:
			return fmt.Errorf("answered by a record of article %d stating no comment count", id)
		}
		return nil
	}
	var tree []devtoComment
	if err := json.Unmarshal(body, &tree); err != nil {
		return notJSON
	}
	var walk func([]devtoComment) error
	walk = func(comments []devtoComment) error {
		for index := range comments {
			if comments[index].IDCode == "" {
				return fmt.Errorf("answered by a comment tree holding a comment with no id")
			}
			if err := walk(comments[index].Children); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(tree)
}

// devtoPosts flattens a comment tree to socialPosts, each after its parent.
func devtoPosts(comments []devtoComment, parent, articleURL string, out []socialPost) []socialPost {
	for index := range comments {
		comment := &comments[index]
		post := socialPost{
			id: comment.IDCode, parent: parent, posted: githubPosted(comment.CreatedAt),
			author: unknownAuthor, link: articleURL,
		}
		if comment.User != nil && comment.User.Username != "" {
			post.author = "@" + comment.User.Username
			post.link = "https://" + devtoHost + "/" + comment.User.Username + "/comment/" + comment.IDCode
		}
		post.body = htmlMarkdown(comment.BodyHTML)
		if post.body == devtoDeletedBody && post.author == unknownAuthor {
			post.unavailable = socialDeleted
		}
		out = devtoPosts(comment.Children, comment.IDCode, articleURL, append(out, post))
	}
	return out
}

// devtoBody is an article's Markdown without its front matter.
func devtoBody(markdown string) string {
	markdown = strings.TrimSpace(strings.ReplaceAll(markdown, "\r\n", "\n"))
	if rest, found := strings.CutPrefix(markdown, "---\n"); found {
		if _, body, closed := strings.Cut(rest, "\n---\n"); closed {
			return strings.TrimSpace(body)
		}
	}
	return markdown
}

// extractDevtoArticle renders a dev.to article and its comments from the API
// answers kept in its page.
func extractDevtoArticle(doc *html.Node, page *url.URL) (siteExtraction, bool) {
	state, ok := devtoPageOf(doc, page)
	if !ok {
		return siteExtraction{}, false
	}
	article := state.article
	if article == nil {
		return siteExtraction{unrendered: "dev.to article: the article's API record was not loaded " +
			"(its comments not read from the API; the page is stored as dev.to served it)"}, false
	}
	link := article.URL
	if link == "" {
		link = page.String()
	}
	thread := socialThread{
		kind:  "dev.to article",
		title: article.Title,
		post: socialPost{
			id: strconv.Itoa(article.ID), author: unknownAuthor, posted: githubPosted(article.PublishedAt),
			link: link, body: devtoBody(article.BodyMarkdown), stated: *article.CommentsCount,
		},
		repliesRead: state.read,
		notServed: "dev.to's comment tree did not hold them (hidden by the article's author, or shown only " +
			"to a reader signed in)",
		unavailableUnstated: true,
	}
	if article.User != nil && article.User.Username != "" {
		thread.post.author = "@" + article.User.Username
	}
	if state.read {
		thread.replies = devtoPosts(state.comments, thread.post.id, link, nil)
	}
	markdown, partial := thread.render()
	return siteExtraction{markdown: markdown, partial: partial, apiRecord: true}, true
}
