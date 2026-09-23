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

// The Lemmy post extractor. Lemmy runs on many domains: a page is known by
// its markup (lemmy-ui's window.isoData script) or, for the largest
// instances, by its host — lemmy.world serves a non-browser a challenge wall
// in the page's place, which holds no markup to know it by. The page's
// comments are drawn in the browser from the instance's public API, which the
// extractor reads instead, on the page's own host, unauthenticated, through
// loaders.go's budget: the post's record (/api/v3/post?id=: its stated
// comment count), then the comment list, oldest first, lemmyPerPage a page,
// until a short page ends it (the list's max_depth form answers a whole tree
// but caps it, so it is not used). Each answer is checked to be this post's
// before it is kept in the page as a harvester-lemmy-answer element, so a
// later conversion replays it like any followed loader. A post whose record
// never loaded is not claimed: the page goes the generic path, the gap named
// in its partial marker. The comments render as a tree (social_thread.go),
// each under its parent by its path; a comment deleted by its author or
// removed by a moderator stays a mark its replies nest under, and the
// instance's count leaves it out.

const (
	lemmyAnswerTag    = "harvester-lemmy-answer"
	lemmyKindPost     = "post"
	lemmyKindComments = "comments"
	lemmyPerPage      = 50
)

// lemmyHosts are the instances claimed by their host alone.
var lemmyHosts = []string{
	"lemmy.world", "lemmy.ml", "sh.itjust.works", "lemmy.dbzer0.com", "programming.dev", "feddit.org",
	"lemmy.zip", "lemmy.ca",
}

type lemmyPerson struct {
	Name    string `json:"name"`
	ActorID string `json:"actor_id"`
}

type lemmyPostView struct {
	PostView *struct {
		Post struct {
			ID        int64  `json:"id"`
			Name      string `json:"name"`
			Body      string `json:"body"`
			URL       string `json:"url"`
			APID      string `json:"ap_id"`
			Published string `json:"published"`
		} `json:"post"`
		Creator   lemmyPerson `json:"creator"`
		Community lemmyPerson `json:"community"`
		Counts    struct {
			Comments *int `json:"comments"`
		} `json:"counts"`
	} `json:"post_view"`
}

type lemmyCommentView struct {
	Comment struct {
		ID        int64  `json:"id"`
		PostID    int64  `json:"post_id"`
		Content   string `json:"content"`
		Deleted   bool   `json:"deleted"`
		Removed   bool   `json:"removed"`
		Published string `json:"published"`
		APID      string `json:"ap_id"`
		Path      string `json:"path"`
	} `json:"comment"`
	Creator lemmyPerson `json:"creator"`
}

type lemmyCommentPage struct {
	Comments []lemmyCommentView `json:"comments"`
}

// isLemmy reports a page lemmy-ui served: its window.isoData script.
func isLemmy(doc *html.Node) bool {
	var found bool
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if found {
			return
		}
		if node.Type == html.ElementNode && node.DataAtom == atom.Script {
			found = strings.Contains(rawText(node), "window.isoData")
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return found
}

// lemmyPage is what a post page holds of its API answers.
type lemmyPage struct {
	origin string
	id     int64
	post   *lemmyPostView
	pages  map[int][]lemmyCommentView
	// dropped holds the keys of loaders dropped without an answer kept.
	dropped map[string]bool
}

func lemmyPageOf(page *url.URL) (lemmyPage, bool) {
	parts := strings.Split(strings.Trim(page.Path, "/"), "/")
	if len(parts) != 2 || parts[0] != "post" || (page.Scheme != schemeHTTPS && page.Scheme != schemeHTTP) {
		return lemmyPage{}, false
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || id <= 0 {
		return lemmyPage{}, false
	}
	return lemmyPage{
		origin: page.Scheme + "://" + page.Host, id: id, pages: map[int][]lemmyCommentView{},
		dropped: map[string]bool{},
	}, true
}

func lemmyStateOf(doc *html.Node, page *url.URL) (lemmyPage, bool) {
	state, ok := lemmyPageOf(page)
	if !ok {
		return lemmyPage{}, false
	}
	for _, node := range keptAnswers(doc, lemmyAnswerTag) {
		if nodeAttr(node, "dropped") != "" {
			state.dropped[nodeAttr(node, "key")] = true
			continue
		}
		var err error
		switch kind := nodeAttr(node, "kind"); kind {
		case lemmyKindPost:
			var post lemmyPostView
			if err = json.Unmarshal([]byte(rawText(node)), &post); err == nil {
				state.post = &post
			}
		case lemmyKindComments:
			var answer lemmyCommentPage
			number, _ := strconv.Atoi(nodeAttr(node, "page"))
			if err = json.Unmarshal([]byte(rawText(node)), &answer); err == nil {
				state.pages[number] = answer.Comments
			}
		}
		if err != nil {
			obs.Logger(context.Background()).Warn("harvest: a kept Lemmy answer no longer decodes; left out",
				"kind", nodeAttr(node, "kind"), obs.FieldErr, err.Error())
		}
	}
	return state, true
}

func lemmyKey(target string) string { return "lemmy-api " + target }

func (state lemmyPage) commentsTarget(number int) string {
	return fmt.Sprintf("%s/api/v3/comment/list?post_id=%d&type_=All&sort=Old&limit=%d&page=%d",
		state.origin, state.id, lemmyPerPage, number)
}

// lemmyLoaders names the API answer the post page still lacks: its record,
// then the first comment page not kept before a short page ends the list.
func lemmyLoaders(doc *html.Node, page *url.URL) []pageLoader {
	state, ok := lemmyStateOf(doc, page)
	if !ok {
		return nil
	}
	recordTarget := fmt.Sprintf("%s/api/v3/post?id=%d", state.origin, state.id)
	if state.post == nil {
		if state.dropped[lemmyKey(recordTarget)] {
			return nil
		}
		return []pageLoader{state.loader(doc, lemmyKindPost, 0, recordTarget, "the post's API record")}
	}
	for number := 1; ; number++ {
		comments, kept := state.pages[number]
		if !kept {
			target := state.commentsTarget(number)
			if state.dropped[lemmyKey(target)] {
				return nil
			}
			return []pageLoader{state.loader(doc, lemmyKindComments, number, target,
				fmt.Sprintf("comments page %d", number))}
		}
		if len(comments) < lemmyPerPage {
			return nil
		}
	}
}

func (state lemmyPage) loader(doc *html.Node, kind string, number int, target, label string) pageLoader {
	key := lemmyKey(target)
	return pageLoader{
		key:     key,
		label:   label,
		method:  http.MethodGet,
		target:  target,
		headers: map[string]string{headerAccept: mediaTypeJSON},
		graft: func(body []byte, contentType string) error {
			if err := state.check(kind, label, body, contentType); err != nil {
				return err
			}
			keepAnswer(doc, lemmyAnswerTag, key, kind, number, body)
			return nil
		},
		drop: func() { keepAnswer(doc, lemmyAnswerTag, key, kind, number, nil) },
	}
}

// check proves an answer is this post's record, or a page of its comments.
func (state lemmyPage) check(kind, label string, body []byte, contentType string) error {
	notJSON := fmt.Errorf("answered by a %s body that is not the API's JSON for %s",
		discourseContentType(contentType), label)
	if kind == lemmyKindPost {
		var post lemmyPostView
		switch err := json.Unmarshal(body, &post); {
		case err != nil || post.PostView == nil:
			return notJSON
		case post.PostView.Post.ID != state.id:
			return fmt.Errorf("answered by the record of post %d, not %d", post.PostView.Post.ID, state.id)
		case post.PostView.Counts.Comments == nil:
			return fmt.Errorf("answered by a record of post %d stating no comment count", state.id)
		}
		return nil
	}
	var answer lemmyCommentPage
	if err := json.Unmarshal(body, &answer); err != nil || !bytesHasKey(body, "comments") {
		return notJSON
	}
	for index := range answer.Comments {
		if comment := answer.Comments[index].Comment; comment.ID == 0 || comment.PostID != state.id {
			return fmt.Errorf("answered by a list holding a comment (%d) of another post", comment.ID)
		}
	}
	return nil
}

// lemmyAuthor is a person as name@instance, the instance read off its actor id.
func lemmyAuthor(person lemmyPerson) string {
	if person.Name == "" {
		return unknownAuthor
	}
	if actor, err := url.Parse(person.ActorID); err == nil && actor.Host != "" {
		return "@" + person.Name + "@" + actor.Host
	}
	return "@" + person.Name
}

// extractLemmyPost renders a Lemmy post and its comments from the API answers
// kept in its page.
func extractLemmyPost(doc *html.Node, page *url.URL) (siteExtraction, bool) {
	state, ok := lemmyStateOf(doc, page)
	if !ok {
		return siteExtraction{}, false
	}
	if state.post == nil {
		return siteExtraction{unrendered: "lemmy post: the post's API record was not loaded " +
			"(its comments not read from the API; the page is stored as the instance served it)"}, false
	}
	view := state.post.PostView
	postID := "post " + strconv.FormatInt(state.id, 10)
	var body []string
	if text := strings.TrimSpace(view.Post.Body); text != "" {
		body = append(body, text)
	}
	if view.Post.URL != "" {
		body = append(body, "[link]("+view.Post.URL+")")
	}
	link := view.Post.APID
	if link == "" {
		link = page.String()
	}
	thread := socialThread{
		kind:  "lemmy post",
		title: view.Post.Name,
		post: socialPost{
			id: postID, author: lemmyAuthor(view.Creator), posted: githubPosted(view.Post.Published),
			link: link, body: strings.Join(body, "\n\n"), stated: *view.Counts.Comments,
		},
		notServed: "the instance's comment list did not hold them (a comment of a remote instance it has " +
			"not received, or a count not yet updated)",
		unavailableUnstated: true,
	}
	if community := view.Community.Name; community != "" {
		thread.title += " (" + lemmyAuthor(view.Community)[1:] + ")"
	}
	seen := map[int64]bool{}
	for number := 1; ; number++ {
		comments, kept := state.pages[number]
		if !kept {
			if number > 1 || len(state.pages) > 0 {
				thread.gaps = append(thread.gaps,
					fmt.Sprintf("comments page %d not loaded (the list not read to its end)", number))
			}
			break
		}
		thread.repliesRead = true
		for index := range comments {
			entry := &comments[index]
			if seen[entry.Comment.ID] {
				continue
			}
			seen[entry.Comment.ID] = true
			thread.replies = append(thread.replies, lemmyPost(entry, postID))
		}
		if len(comments) < lemmyPerPage {
			break
		}
	}
	markdown, partial := thread.render()
	return siteExtraction{markdown: markdown, partial: partial, apiRecord: true}, true
}

// lemmyPost maps one comment to a socialPost, its parent read off its path
// ("0.<ancestor ids>.<own id>").
func lemmyPost(entry *lemmyCommentView, postID string) socialPost {
	comment := &entry.Comment
	post := socialPost{
		id: strconv.FormatInt(comment.ID, 10), parent: postID, author: lemmyAuthor(entry.Creator),
		posted: githubPosted(comment.Published), link: comment.APID, body: strings.TrimSpace(comment.Content),
	}
	if path := strings.Split(comment.Path, "."); len(path) > 2 {
		post.parent = path[len(path)-2]
	}
	switch {
	case comment.Removed:
		post.unavailable = "removed by a moderator"
	case comment.Deleted:
		post.unavailable = socialDeleted
	}
	return post
}
