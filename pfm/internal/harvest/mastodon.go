package harvest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// The Mastodon status extractor. Mastodon runs on many domains, so a page is
// known by its markup — the web app's mount point (div#mastodon) and its
// initial-state script — never by its host. The page is an app shell: the
// status and its replies are drawn in the browser from the instance's public
// REST API, which the extractor reads instead, on the page's own host,
// unauthenticated, through loaders.go's budget: the status's record
// (/api/v1/statuses/{id}, its stated replies_count) and its context
// (/api/v1/statuses/{id}/context: the ancestors and the descendants). Each
// answer is checked to be this status's before it is kept in the page as a
// harvester-mastodon-answer element, so a later conversion replays it like
// any followed loader. A status whose record never loaded is not claimed: the
// page goes the generic path, the gap named in its partial marker. The
// content HTML becomes Markdown; the thread renders as a tree (social_thread.go)
// and reconciles each loaded post's stated replies against the replies loaded.
// An instance serves an unauthenticated reader a bounded context (60
// descendants on current releases) and never a reply that is not public:
// what it did not serve is named.

const (
	mastodonAnswerTag  = "harvester-mastodon-answer"
	mastodonKindStatus = "status"
	mastodonKindCtx    = "context"
)

type mastodonAccount struct {
	Acct        string `json:"acct"`
	DisplayName string `json:"display_name"`
}

type mastodonMedia struct {
	Type        string  `json:"type"`
	URL         string  `json:"url"`
	Description *string `json:"description"`
}

type mastodonStatus struct {
	ID           string           `json:"id"`
	InReplyToID  *string          `json:"in_reply_to_id"`
	URL          *string          `json:"url"`
	URI          string           `json:"uri"`
	CreatedAt    string           `json:"created_at"`
	Content      string           `json:"content"`
	SpoilerText  string           `json:"spoiler_text"`
	RepliesCount *int             `json:"replies_count"`
	Account      *mastodonAccount `json:"account"`
	Media        []mastodonMedia  `json:"media_attachments"`
}

type mastodonContext struct {
	Ancestors   []mastodonStatus `json:"ancestors"`
	Descendants []mastodonStatus `json:"descendants"`
}

// isMastodon reports a page of Mastodon's web app: its mount point and its
// initial-state script.
func isMastodon(doc *html.Node) bool {
	mount := firstWithAttr(doc, "id", "mastodon", nil)
	state := firstWithAttr(doc, "id", "initial-state", nil)
	return mount != nil && mount.DataAtom == atom.Div && state != nil && state.DataAtom == atom.Script
}

// mastodonStatusID reads a status address: /@<account>/<id> (the account may
// name a remote instance) or /users/<name>/statuses/<id>.
func mastodonStatusID(path string) (string, bool) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	var id string
	switch {
	case len(parts) == 2 && strings.HasPrefix(parts[0], "@") && len(parts[0]) > 1:
		id = parts[1]
	case len(parts) == 4 && parts[0] == "users" && parts[2] == "statuses":
		id = parts[3]
	default:
		return "", false
	}
	if id == "" || strings.Trim(id, "0123456789") != "" {
		return "", false
	}
	return id, true
}

// mastodonPage is what a status page holds of its API answers.
type mastodonPage struct {
	origin  string
	id      string
	status  *mastodonStatus
	context *mastodonContext
	dropped map[string]bool
}

func mastodonPageOf(doc *html.Node, page *url.URL) (mastodonPage, bool) {
	id, ok := mastodonStatusID(page.Path)
	if !ok || !isMastodon(doc) || (page.Scheme != schemeHTTPS && page.Scheme != schemeHTTP) {
		return mastodonPage{}, false
	}
	state := mastodonPage{origin: page.Scheme + "://" + page.Host, id: id, dropped: map[string]bool{}}
	for _, node := range keptAnswers(doc, mastodonAnswerTag) {
		if nodeAttr(node, "dropped") != "" {
			state.dropped[nodeAttr(node, "key")] = true
			continue
		}
		body := []byte(rawText(node))
		var err error
		switch kind := nodeAttr(node, "kind"); kind {
		case mastodonKindStatus:
			var status mastodonStatus
			if err = json.Unmarshal(body, &status); err == nil {
				state.status = &status
			}
		case mastodonKindCtx:
			var answer mastodonContext
			if err = json.Unmarshal(body, &answer); err == nil {
				state.context = &answer
			}
		}
		if err != nil {
			obs.Logger(context.Background()).Warn("harvest: a kept Mastodon answer no longer decodes; left out",
				"kind", nodeAttr(node, "kind"), obs.FieldErr, err.Error())
		}
	}
	return state, true
}

func (state mastodonPage) target(kind string) string {
	target := state.origin + "/api/v1/statuses/" + state.id
	if kind == mastodonKindCtx {
		target += "/context"
	}
	return target
}

func mastodonKey(target string) string { return "mastodon-api " + target }

// mastodonLoaders names the API answers the status page still lacks: its
// record, then its context.
func mastodonLoaders(doc *html.Node, page *url.URL) []pageLoader {
	state, ok := mastodonPageOf(doc, page)
	if !ok {
		return nil
	}
	var loaders []pageLoader
	for _, want := range []struct {
		kind, label string
		kept        bool
	}{
		{mastodonKindStatus, "the status's API record", state.status != nil},
		{mastodonKindCtx, "the status's context (its replies)", state.context != nil},
	} {
		target := state.target(want.kind)
		key := mastodonKey(target)
		if want.kind == mastodonKindCtx && state.status == nil {
			// The context of a status whose record never proved it is not asked.
			break
		}
		if want.kept || state.dropped[key] {
			continue
		}
		kind := want.kind
		loaders = append(loaders, pageLoader{
			key:     key,
			label:   want.label,
			method:  http.MethodGet,
			target:  target,
			headers: map[string]string{headerAccept: mediaTypeJSON},
			graft: func(body []byte, contentType string) error {
				if err := state.check(kind, body, contentType); err != nil {
					return err
				}
				keepAnswer(doc, mastodonAnswerTag, key, kind, 0, body)
				return nil
			},
			drop: func() { keepAnswer(doc, mastodonAnswerTag, key, kind, 0, nil) },
		})
	}
	return loaders
}

// check proves an API answer is this status's record or context.
func (state mastodonPage) check(kind string, body []byte, contentType string) error {
	notJSON := fmt.Errorf("answered by a %s body that is not the API's JSON for status %s",
		discourseContentType(contentType), state.id)
	if kind == mastodonKindStatus {
		var status mastodonStatus
		if err := json.Unmarshal(body, &status); err != nil || status.ID == "" {
			return notJSON
		}
		if status.ID != state.id {
			return fmt.Errorf("answered by the record of status %s, not %s", status.ID, state.id)
		}
		if status.RepliesCount == nil {
			return fmt.Errorf("answered by a record of status %s stating no reply count", status.ID)
		}
		return nil
	}
	var answer mastodonContext
	if err := json.Unmarshal(body, &answer); err != nil || !bytesHasKey(body, "descendants") {
		return notJSON
	}
	known := map[string]bool{state.id: true}
	for index := range answer.Descendants {
		known[answer.Descendants[index].ID] = true
	}
	for index := range answer.Descendants {
		reply := &answer.Descendants[index]
		if reply.ID == "" || reply.InReplyToID == nil || !known[*reply.InReplyToID] {
			return fmt.Errorf("answered by a context holding a status (%s) outside this thread", reply.ID)
		}
	}
	return nil
}

// bytesHasKey reports a JSON object answer naming key at its top level.
func bytesHasKey(body []byte, key string) bool {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return false
	}
	_, ok := fields[key]
	return ok
}

// mastodonPost maps one status to a socialPost.
func mastodonPost(status *mastodonStatus) socialPost {
	post := socialPost{id: status.ID, posted: githubPosted(status.CreatedAt), author: unknownAuthor, link: status.URI}
	if status.InReplyToID != nil {
		post.parent = *status.InReplyToID
	}
	if status.URL != nil && *status.URL != "" {
		post.link = *status.URL
	}
	if status.Account != nil && status.Account.Acct != "" {
		post.author = "@" + status.Account.Acct
	}
	if status.RepliesCount != nil {
		post.stated = *status.RepliesCount
	}
	var body []string
	if warning := strings.TrimSpace(status.SpoilerText); warning != "" {
		body = append(body, "**Content warning:** "+warning)
	}
	if text := htmlMarkdown(status.Content); text != "" {
		body = append(body, text)
	}
	for index := range status.Media {
		media := &status.Media[index]
		label := media.Type
		if media.Description != nil && strings.TrimSpace(*media.Description) != "" {
			label += ": " + strings.Join(strings.Fields(*media.Description), " ")
		}
		body = append(body, "["+label+"]("+media.URL+")")
	}
	post.body = strings.Join(body, "\n\n")
	return post
}

// extractMastodonStatus renders a Mastodon status and its thread from the API
// answers kept in its page.
func extractMastodonStatus(doc *html.Node, page *url.URL) (siteExtraction, bool) {
	state, ok := mastodonPageOf(doc, page)
	if !ok {
		return siteExtraction{}, false
	}
	if state.status == nil {
		return siteExtraction{unrendered: "mastodon status: the status's API record was not loaded " +
			"(its replies not read from the API; the page is stored as the instance served it)"}, false
	}
	thread := socialThread{
		kind: "mastodon status",
		post: mastodonPost(state.status),
		notServed: "the instance's public context answer did not hold them (it serves an unauthenticated " +
			"reader a bounded number of descendants, and no reply that is not public)",
	}
	name := thread.post.author
	if state.status.Account != nil && state.status.Account.DisplayName != "" {
		name = state.status.Account.DisplayName + " (" + thread.post.author + ")"
	}
	thread.title = name + " on " + page.Hostname()
	if state.context == nil {
		thread.gaps = append(thread.gaps, "the status's context API answer was not loaded")
	} else {
		thread.repliesRead = true
		for index := range state.context.Ancestors {
			thread.ancestors = append(thread.ancestors, mastodonPost(&state.context.Ancestors[index]))
		}
		for index := range state.context.Descendants {
			thread.replies = append(thread.replies, mastodonPost(&state.context.Descendants[index]))
		}
	}
	markdown, partial := thread.render()
	return siteExtraction{markdown: markdown, partial: partial, apiRecord: true}, true
}
