package harvest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

// Reaching past a Mastodon context's cap. An instance serves an
// unauthenticated reader a bounded context (60 descendants, depth first), so a
// busy status's later replies are cut. The extractor reaches more through
// public, same-host means only, each request through loaders.go's budget:
//   - a loaded status stating more replies than are loaded under it gets its
//     own context (/api/v1/statuses/{id}/context), a subtree with its own cap;
//   - one still short, when it is the page host's own status, gets its
//     ActivityPub replies collection (its uri + /replies, Accept
//     application/activity+json), paged by first/next: each reply there that
//     is the host's own and not yet loaded is read as an API record
//     (/api/v1/statuses/{id}), then treated like any loaded status.
//
// A reply hosted on another instance appears in a collection as a remote uri;
// it counts as loaded when the host already served it in some context (the
// same uri), and is otherwise named "remote, not fetched": the host's
// lookup of a remote uri (search) needs a login, and another instance is never
// asked.

const (
	// mastodonKindReply is a reply's API record, found in a replies collection.
	mastodonKindReply = "reply"
	// mastodonKindReplies is one page of a status's ActivityPub replies
	// collection (or the collection itself, embedding its first page).
	mastodonKindReplies   = "replies"
	mediaTypeActivityJSON = "application/activity+json"
	mastodonRemoteState   = "remote, not fetched (hosted on another instance and never served by this one)"
)

// apRepliesPage is an ActivityPub replies collection or one of its pages.
type apRepliesPage struct {
	ID     string            `json:"id"`
	Type   string            `json:"type"`
	PartOf string            `json:"partOf"`
	Next   string            `json:"next"`
	First  json.RawMessage   `json:"first"`
	Items  []json.RawMessage `json:"items"`
}

// apItemURI reads a collection item: a remote reply's uri, or the host's own
// reply as its Note object (its id, and the uri it answers).
func apItemURI(raw json.RawMessage) (uri, inReplyTo string) {
	if err := json.Unmarshal(raw, &uri); err == nil {
		return uri, ""
	}
	var note struct {
		ID        string `json:"id"`
		InReplyTo string `json:"inReplyTo"`
	}
	if err := json.Unmarshal(raw, &note); err != nil {
		return "", ""
	}
	return note.ID, note.InReplyTo
}

// firstPage is the collection's first page: embedded (its target and itself)
// or named by its target only.
func (page apRepliesPage) firstPage() (target string, embedded *apRepliesPage) {
	if len(page.First) == 0 {
		return "", nil
	}
	if err := json.Unmarshal(page.First, &target); err == nil {
		return target, nil
	}
	var first apRepliesPage
	if err := json.Unmarshal(page.First, &first); err != nil || first.ID == "" {
		return "", nil
	}
	return first.ID, &first
}

// repliesCollection is the replies collection address of a status's uri.
func repliesCollection(uri string) string { return uri + "/replies" }

// isOwn reports a uri on the page's own host.
func (state mastodonPage) isOwn(uri string) bool {
	parsed, err := url.Parse(uri)
	return err == nil && parsed.Scheme+"://"+parsed.Host == state.origin
}

// mastodonTree is the loaded thread: the replies in load order, once each, and
// the remote replies the collections named that no loaded status is.
type mastodonTree struct {
	replies  []*mastodonStatus
	byID     map[string]*mastodonStatus
	byURI    map[string]bool
	children map[string]int
}

func (state mastodonPage) tree() mastodonTree {
	tree := mastodonTree{byID: map[string]*mastodonStatus{}, byURI: map[string]bool{}, children: map[string]int{}}
	if state.status != nil {
		tree.byID[state.id] = state.status
		tree.byURI[state.status.URI] = true
	}
	for index := range state.found {
		status := &state.found[index]
		if tree.byID[status.ID] != nil || status.InReplyToID == nil {
			continue
		}
		tree.byID[status.ID] = status
		tree.byURI[status.URI] = true
		tree.replies = append(tree.replies, status)
		tree.children[*status.InReplyToID]++
	}
	return tree
}

// short lists the loaded statuses stating more replies than are loaded under
// them, the page's status first.
func (tree mastodonTree) short(root *mastodonStatus) []*mastodonStatus {
	var short []*mastodonStatus
	for _, status := range append([]*mastodonStatus{root}, tree.replies...) {
		if status.RepliesCount != nil && tree.children[status.ID] < *status.RepliesCount {
			short = append(short, status)
		}
	}
	return short
}

// collectionItems walks a status's replies collection as far as its pages are
// kept: the item uris read (with the uri each answers, "" for a bare uri) and
// the target of the first page not yet kept ("" when the walk is complete).
func (state mastodonPage) collectionItems(status *mastodonStatus) (items [][2]string, unread string) {
	target, seen := repliesCollection(status.URI), map[string]bool{}
	for target != "" && !seen[target] {
		seen[target] = true
		page, ok := state.pages[target]
		if !ok {
			return items, target
		}
		for _, raw := range page.Items {
			if uri, parent := apItemURI(raw); uri != "" {
				items = append(items, [2]string{uri, parent})
			}
		}
		target = page.Next
		if first, _ := page.firstPage(); first != "" {
			target = first
		}
	}
	return items, ""
}

// replyLoaders names what the thread still lacks past the page status's own
// context: each short status's context, then its collection's next page, then
// the records of its own-host replies the collection named and no answer holds.
func (state mastodonPage) replyLoaders(doc *html.Node) []pageLoader {
	tree := state.tree()
	var loaders []pageLoader
	for _, status := range tree.short(state.status) {
		if _, kept := state.contexts[status.ID]; !kept && status.ID != state.id {
			target := state.origin + "/api/v1/statuses/" + status.ID + "/context"
			loaders = append(loaders, state.loader(doc, mastodonKindCtx, status, target,
				"a reply's context (its replies)", mediaTypeJSON))
			continue
		}
		if !state.isOwn(status.URI) {
			continue
		}
		items, unread := state.collectionItems(status)
		if unread != "" {
			loaders = append(loaders, state.loader(doc, mastodonKindReplies, status, unread,
				"a status's ActivityPub replies collection", mediaTypeActivityJSON))
		}
		for _, item := range items {
			id, ok := "", false
			if parsed, err := url.Parse(item[0]); err == nil && state.isOwn(item[0]) {
				id, ok = mastodonStatusID(parsed.Path)
			}
			if !ok || tree.byURI[item[0]] || tree.byID[id] != nil {
				continue
			}
			loaders = append(loaders, state.loader(doc, mastodonKindReply, status,
				state.origin+"/api/v1/statuses/"+id, "a reply's API record", mediaTypeJSON))
		}
	}
	var fresh []pageLoader
	for _, loader := range loaders {
		if !state.dropped[loader.key] && !state.kept[loader.key] {
			fresh = append(fresh, loader)
		}
	}
	return fresh
}

// remoteReplies are the socialPosts of the remote replies the collections
// named that no loaded status is: "remote, not fetched".
func (state mastodonPage) remoteReplies(tree mastodonTree) []socialPost {
	var remote []socialPost
	named := map[string]bool{}
	for _, status := range append([]*mastodonStatus{state.status}, tree.replies...) {
		if !state.isOwn(status.URI) {
			continue
		}
		items, _ := state.collectionItems(status)
		for _, item := range items {
			if tree.byURI[item[0]] || named[item[0]] || state.isOwn(item[0]) {
				continue
			}
			named[item[0]] = true
			remote = append(remote, socialPost{
				id: item[0], parent: status.ID, link: item[0],
				unavailable: mastodonRemoteState,
			})
		}
	}
	return remote
}

// loader is one API request of the thread about status (its subject: the
// status whose context or collection is asked, or the parent of the reply
// whose record is).
func (state mastodonPage) loader(doc *html.Node, kind string, status *mastodonStatus, target, label,
	accept string,
) pageLoader {
	key := mastodonKey(target)
	return pageLoader{
		key:     key,
		label:   label,
		method:  http.MethodGet,
		target:  target,
		headers: map[string]string{headerAccept: accept},
		graft: func(body []byte, contentType string) error {
			if err := state.checkReply(kind, status, target, body, contentType); err != nil {
				return err
			}
			keepAnswer(doc, mastodonAnswerTag, key, kind, 0, body)
			return nil
		},
		drop: func() { keepAnswer(doc, mastodonAnswerTag, key, kind, 0, nil) },
	}
}

// checkReply proves an answer is what its loader asked about status.
func (state mastodonPage) checkReply(kind string, status *mastodonStatus, target string, body []byte,
	contentType string,
) error {
	switch kind {
	case mastodonKindStatus:
		return checkStatus(status.ID, body, contentType)
	case mastodonKindCtx:
		return checkContext(status.ID, body, contentType)
	case mastodonKindReply:
		id := target[strings.LastIndex(target, "/")+1:]
		if err := checkStatus(id, body, contentType); err != nil {
			return err
		}
		var reply mastodonStatus
		_ = json.Unmarshal(body, &reply) // checkStatus decoded it
		if reply.InReplyToID == nil || *reply.InReplyToID != status.ID {
			return fmt.Errorf("answered by status %s, which does not answer %s", id, status.ID)
		}
		return nil
	}
	var page apRepliesPage
	collection := repliesCollection(status.URI)
	if err := json.Unmarshal(body, &page); err != nil || page.Type == "" {
		return fmt.Errorf("answered by a %s body that is not the ActivityPub replies of status %s",
			discourseContentType(contentType), status.ID)
	}
	if owner, _, _ := strings.Cut(page.ID, "?"); owner != collection && page.PartOf != collection {
		return fmt.Errorf("answered by the collection %s, not the replies of status %s", page.ID, status.ID)
	}
	for _, raw := range page.Items {
		if uri, parent := apItemURI(raw); uri == "" || (parent != "" && parent != status.URI) {
			return fmt.Errorf("answered by a replies page holding an item (%s) that does not answer status %s",
				uri, status.ID)
		}
	}
	return nil
}
