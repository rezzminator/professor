package harvest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// The Bluesky post extractor. A post page on bsky.app is an app shell whose
// noscript block holds the post's text alone; the thread is drawn in the
// browser from the AT Protocol's AppView. The extractor reads it instead from
// the public AppView (public.api.bsky.app, an API host the extractor names),
// unauthenticated, through loaders.go's budget: the author's handle resolved
// to its DID (com.atproto.identity.resolveHandle; an address naming a DID
// skips it), then the post's thread (app.bsky.feed.getPostThread, every depth
// and every parent the API serves). Each answer is checked to be this post's
// before it is kept in the page as a harvester-bluesky-answer element, so a
// later conversion replays it like any followed loader. A post whose thread
// never loaded is not claimed: the page goes the generic path, the gap named
// in its partial marker. The thread renders as a tree (social_thread.go) and
// reconciles each loaded post's stated replyCount against the replies loaded;
// what the AppView leaves out for a logged-out reader is named.

const (
	bskyHost    = "bsky.app"
	bskyAPIHost = "public.api.bsky.app"
	bskyAPI     = "https://" + bskyAPIHost + "/xrpc/"
	// bskyDepth is the deepest reply level and the highest parent level the
	// thread is asked for: the API's maximum.
	bskyDepth       = 1000
	bskyAnswerTag   = "harvester-bluesky-answer"
	bskyKindHandle  = "handle"
	bskyKindThread  = "thread"
	bskyThreadType  = "app.bsky.feed.defs#threadViewPost"
	bskyLinkFeature = "app.bsky.richtext.facet#link"
)

type bskyAuthor struct {
	DID         string `json:"did"`
	Handle      string `json:"handle"`
	DisplayName string `json:"displayName"`
}

type bskyFacet struct {
	Index struct {
		ByteStart int `json:"byteStart"`
		ByteEnd   int `json:"byteEnd"`
	} `json:"index"`
	Features []struct {
		Type string `json:"$type"`
		URI  string `json:"uri"`
	} `json:"features"`
}

type bskyPost struct {
	URI    string      `json:"uri"`
	Author *bskyAuthor `json:"author"`
	Record struct {
		Text      string      `json:"text"`
		CreatedAt string      `json:"createdAt"`
		Facets    []bskyFacet `json:"facets"`
	} `json:"record"`
	Embed *struct {
		Images []struct {
			Fullsize string `json:"fullsize"`
			Alt      string `json:"alt"`
		} `json:"images"`
		External *struct {
			URI   string `json:"uri"`
			Title string `json:"title"`
		} `json:"external"`
	} `json:"embed"`
	ReplyCount *int   `json:"replyCount"`
	IndexedAt  string `json:"indexedAt"`
}

// bskyNode is one node of a thread: a post, or a mark for one not readable.
type bskyNode struct {
	Type    string     `json:"$type"`
	URI     string     `json:"uri"`
	Post    *bskyPost  `json:"post"`
	Parent  *bskyNode  `json:"parent"`
	Replies []bskyNode `json:"replies"`
}

// bskyAddress is the post a page's address names: /profile/<actor>/post/<rkey>.
type bskyAddress struct{ actor, rkey string }

func bskyAddressOf(page *url.URL) (bskyAddress, bool) {
	parts := strings.Split(strings.Trim(page.Path, "/"), "/")
	if !strings.EqualFold(page.Hostname(), bskyHost) || len(parts) != 4 || parts[0] != "profile" ||
		parts[2] != "post" || parts[1] == "" || parts[3] == "" {
		return bskyAddress{}, false
	}
	return bskyAddress{actor: parts[1], rkey: parts[3]}, true
}

// bskyPage is what a post page holds of its API answers.
type bskyPage struct {
	address bskyAddress
	did     string
	thread  *bskyNode
	dropped map[string]bool
}

func bskyPageOf(doc *html.Node, page *url.URL) (bskyPage, bool) {
	address, ok := bskyAddressOf(page)
	if !ok {
		return bskyPage{}, false
	}
	state := bskyPage{address: address, dropped: map[string]bool{}}
	if strings.HasPrefix(address.actor, "did:") {
		state.did = address.actor
	}
	for _, node := range keptAnswers(doc, bskyAnswerTag) {
		if nodeAttr(node, "dropped") != "" {
			state.dropped[nodeAttr(node, "key")] = true
			continue
		}
		body := []byte(rawText(node))
		var err error
		switch nodeAttr(node, "kind") {
		case bskyKindHandle:
			var answer struct {
				DID string `json:"did"`
			}
			if err = json.Unmarshal(body, &answer); err == nil {
				state.did = answer.DID
			}
		case bskyKindThread:
			var answer struct {
				Thread bskyNode `json:"thread"`
			}
			if err = json.Unmarshal(body, &answer); err == nil {
				state.thread = &answer.Thread
			}
		}
		if err != nil {
			obs.Logger(context.Background()).Warn("harvest: a kept Bluesky answer no longer decodes; left out",
				"kind", nodeAttr(node, "kind"), obs.FieldErr, err.Error())
		}
	}
	return state, true
}

func (state bskyPage) postURI() string {
	return "at://" + state.did + "/app.bsky.feed.post/" + state.address.rkey
}

func bskyKey(target string) string { return "bluesky-api " + target }

// bskyLoaders names the API answers the post page still lacks: the handle's
// DID, then the thread.
func bskyLoaders(doc *html.Node, page *url.URL) []pageLoader {
	state, ok := bskyPageOf(doc, page)
	if !ok || state.thread != nil {
		return nil
	}
	kind, label := bskyKindThread, "the post's thread from the public AppView"
	target := fmt.Sprintf("%sapp.bsky.feed.getPostThread?uri=%s&depth=%d&parentHeight=%d", bskyAPI,
		url.QueryEscape(state.postURI()), bskyDepth, bskyDepth)
	if state.did == "" {
		kind, label = bskyKindHandle, "the author's handle resolved to its DID"
		target = bskyAPI + "com.atproto.identity.resolveHandle?handle=" + url.QueryEscape(state.address.actor)
	}
	key := bskyKey(target)
	if state.dropped[key] {
		return nil
	}
	return []pageLoader{{
		key:     key,
		label:   label,
		method:  http.MethodGet,
		target:  target,
		headers: map[string]string{headerAccept: mediaTypeJSON},
		graft: func(body []byte, contentType string) error {
			if err := state.check(kind, body, contentType); err != nil {
				return err
			}
			keepAnswer(doc, bskyAnswerTag, key, kind, 0, body)
			return nil
		},
		drop: func() { keepAnswer(doc, bskyAnswerTag, key, kind, 0, nil) },
	}}
}

// check proves an API answer is this post's handle resolution or thread.
func (state bskyPage) check(kind string, body []byte, contentType string) error {
	notJSON := fmt.Errorf("answered by a %s body that is not the AppView's JSON for %s",
		discourseContentType(contentType), kind)
	if kind == bskyKindHandle {
		var answer struct {
			DID string `json:"did"`
		}
		if err := json.Unmarshal(body, &answer); err != nil || !strings.HasPrefix(answer.DID, "did:") {
			return notJSON
		}
		return nil
	}
	var answer struct {
		Thread bskyNode `json:"thread"`
	}
	if err := json.Unmarshal(body, &answer); err != nil || answer.Thread.Type == "" {
		return notJSON
	}
	if answer.Thread.Type != bskyThreadType || answer.Thread.Post == nil {
		return fmt.Errorf("answered by a thread whose post is not readable (%s)", answer.Thread.Type)
	}
	if answer.Thread.Post.URI != state.postURI() {
		return fmt.Errorf("answered by the thread of another post (%s)", answer.Thread.Post.URI)
	}
	return nil
}

// bskyText renders a post's text as Markdown: its link facets as links (their
// byte ranges, in order), its line breaks kept.
func bskyText(post *bskyPost) string {
	text := post.Record.Text
	var out strings.Builder
	at := 0
	for _, facet := range post.Record.Facets {
		start, end := facet.Index.ByteStart, facet.Index.ByteEnd
		link := ""
		for _, feature := range facet.Features {
			if feature.Type == bskyLinkFeature {
				link = feature.URI
			}
		}
		if link == "" || start < at || end <= start || end > len(text) {
			continue
		}
		out.WriteString(text[at:start] + "[" + text[start:end] + "](" + link + ")")
		at = end
	}
	out.WriteString(text[at:])
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	for index := range lines {
		lines[index] = strings.TrimRight(lines[index], " ")
	}
	body := strings.ReplaceAll(strings.Join(lines, "  \n"), "  \n  \n", "\n\n")
	if post.Embed != nil {
		for _, image := range post.Embed.Images {
			body += "\n\n![" + image.Alt + "](" + image.Fullsize + ")"
		}
		if external := post.Embed.External; external != nil {
			body += "\n\n[" + external.Title + "](" + external.URI + ")"
		}
	}
	return body
}

// bskySocialPost maps one thread node to a socialPost; parent is the id of the
// node it answers.
func bskySocialPost(node *bskyNode, parent string) socialPost {
	if node.Type != bskyThreadType || node.Post == nil {
		state := "not readable (" + node.Type + ")"
		switch {
		case strings.HasSuffix(node.Type, "#notFoundPost"):
			state = "not found (deleted)"
		case strings.HasSuffix(node.Type, "#blockedPost"):
			state = "blocked (by its author or a moderation list)"
		}
		return socialPost{id: node.URI, parent: parent, link: node.URI, unavailable: state}
	}
	post := node.Post
	item := socialPost{
		id:     post.URI,
		parent: parent,
		author: unknownAuthor,
		link:   post.URI,
		posted: githubPosted(post.Record.CreatedAt),
		body:   bskyText(post),
	}
	if post.Author != nil {
		actor := post.Author.Handle
		if actor == "" || actor == "handle.invalid" {
			actor = post.Author.DID
		}
		item.author = "@" + actor
		item.link = "https://" + bskyHost + "/profile/" + actor + "/post/" + post.URI[strings.LastIndex(post.URI, "/")+1:]
	}
	if post.ReplyCount != nil {
		item.stated = *post.ReplyCount
	}
	return item
}

// extractBlueskyPost renders a Bluesky post and its thread from the API
// answers kept in its page.
func extractBlueskyPost(doc *html.Node, page *url.URL) (siteExtraction, bool) {
	state, ok := bskyPageOf(doc, page)
	if !ok {
		return siteExtraction{}, false
	}
	if state.thread == nil {
		return siteExtraction{unrendered: "bluesky post: the post's thread was not loaded from the public AppView " +
			"(its replies not read; the page is stored as Bluesky served it)"}, false
	}
	root := state.thread
	thread := socialThread{
		kind:        "bluesky post",
		post:        bskySocialPost(root, ""),
		repliesRead: true,
		notServed: "the public AppView's thread did not hold them (it leaves out for a logged-out reader the " +
			"replies deleted, hidden by the author's thread gate or by moderation, and those of accounts that " +
			"limit logged-out viewing, labelled !no-unauthenticated; its unspecced getPostThreadV2 and " +
			"getPostThreadOtherV2 serve a logged-out reader no more)",
	}
	name := thread.post.author
	if root.Post.Author != nil && root.Post.Author.DisplayName != "" {
		name = root.Post.Author.DisplayName + " (" + thread.post.author + ")"
	}
	thread.title = name + " on Bluesky"
	for parent := root.Parent; parent != nil; parent = parent.Parent {
		thread.ancestors = append([]socialPost{bskySocialPost(parent, "")}, thread.ancestors...)
	}
	var walk func(node *bskyNode)
	walk = func(node *bskyNode) {
		for index := range node.Replies {
			reply := &node.Replies[index]
			thread.replies = append(thread.replies, bskySocialPost(reply, node.Post.URI))
			if reply.Post != nil {
				walk(reply)
			}
		}
	}
	walk(root)
	markdown, partial := thread.render()
	return siteExtraction{markdown: markdown, partial: partial, apiRecord: true}, true
}
