package harvest

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// The reply tree of a social post (mastodon.go, bluesky.go). Both sites answer
// a post's thread from their API as posts that each state how many replies
// they have; the extractors map their answers to socialPost and render them
// here, the same way: the post's ancestors, the post, then its replies as a
// tree, each reply under the post it answers. The reconciliation sums the
// replies every loaded post states and counts the replies loaded under them:
// a reply stated but not served, and one served only as an unavailable mark
// (deleted, blocked), are named and flag the artifact partial.

// socialPost is one post of a thread as its site's API answered it.
type socialPost struct {
	id string
	// parent is the id of the post this one answers; "" for the thread's post.
	parent string
	author string
	posted string
	link   string
	body   string
	// stated is how many replies the API states the post has.
	stated int
	// unavailable names why a reply the API lists is not readable ("" when it
	// is): it is counted apart from the loaded replies.
	unavailable string
}

// socialThread is one post's thread, ready to render.
type socialThread struct {
	// kind names the thread in a partial marker ("mastodon status").
	kind  string
	title string
	// ancestors are the posts the thread's post answers, oldest first.
	ancestors []socialPost
	post      socialPost
	// replies are the replies loaded under the post, each after its parent.
	replies []socialPost
	// repliesRead is false when the replies were not read at all.
	repliesRead bool
	// notServed names why a stated reply may be absent from the API's answer.
	notServed string
	// gaps are further gaps the extractor names.
	gaps []string
}

// render is the thread as Markdown and its partial marker ("" when every
// stated reply was loaded).
func (thread socialThread) render() (markdown, partial string) {
	gaps := append([]string(nil), thread.gaps...)
	stated := thread.post.stated
	loaded, unavailable := 0, map[string]int{}
	var states []string
	for index := range thread.replies {
		reply := &thread.replies[index]
		if reply.unavailable != "" {
			if unavailable[reply.unavailable] == 0 {
				states = append(states, reply.unavailable)
			}
			unavailable[reply.unavailable]++
			continue
		}
		loaded++
		stated += reply.stated
	}
	countLine := "**Replies:** "
	switch {
	case !thread.repliesRead:
		countLine += fmt.Sprintf("%d stated · 0 loaded", thread.post.stated)
		if thread.post.stated > 0 {
			gaps = append(gaps, fmt.Sprintf("%d stated repl(ies) not read", thread.post.stated))
		}
	default:
		countLine += fmt.Sprintf("%d stated · %d loaded (%d stated to the post itself, the rest to its replies)",
			stated, loaded, thread.post.stated)
		absent := stated - loaded
		for _, state := range states {
			countLine += fmt.Sprintf(" · %d %s", unavailable[state], state)
			gaps = append(gaps, fmt.Sprintf("%d repl(ies) %s", unavailable[state], state))
			absent -= unavailable[state]
		}
		switch {
		case absent > 0:
			gaps = append(gaps, fmt.Sprintf("%d stated repl(ies) not served: %s", absent, thread.notServed))
		case absent < 0:
			countLine += fmt.Sprintf(" · %d more loaded than stated", -absent)
		}
	}
	if len(gaps) > 0 {
		countLine += " · gaps: " + strings.Join(gaps, "; ")
		partial = fmt.Sprintf("%s: %d of %d stated replies loaded — %s", thread.kind, loaded, stated,
			strings.Join(gaps, "; "))
	}

	var out strings.Builder
	out.WriteString("# " + thread.title + "\n\n")
	out.WriteString("**Author:** " + thread.post.author + " · **Posted:** " + thread.post.posted + "  \n")
	out.WriteString("**Post:** " + thread.post.link + "  \n")
	out.WriteString(countLine + "\n\n")
	if len(thread.ancestors) > 0 {
		out.WriteString("## In reply to\n\n")
		for index := range thread.ancestors {
			writeSocialPost(&out, &thread.ancestors[index], "")
		}
		out.WriteString("\n---\n\n")
	}
	if thread.post.body != "" {
		out.WriteString(thread.post.body + "\n\n")
	}
	out.WriteString("---\n\n## Replies\n\n")
	children := map[string][]*socialPost{}
	for index := range thread.replies {
		reply := &thread.replies[index]
		children[reply.parent] = append(children[reply.parent], reply)
	}
	if len(children[thread.post.id]) == 0 {
		out.WriteString("*No replies are loaded.*\n")
	}
	var walk func(parent, indent string)
	walk = func(parent, indent string) {
		for _, reply := range children[parent] {
			writeSocialPost(&out, reply, indent)
			walk(reply.id, indent+"  ")
		}
	}
	walk(thread.post.id, "")
	return out.String(), partial
}

// writeSocialPost writes one post as a list entry at indent: its header line,
// then its body under it.
func writeSocialPost(out *strings.Builder, post *socialPost, indent string) {
	if post.unavailable != "" {
		out.WriteString(indent + "- *a reply " + post.unavailable + "* · " + post.link + "\n")
		return
	}
	out.WriteString(indent + "- **" + post.author + "** · " + post.posted + " · [" + post.id + "](" + post.link + ")\n")
	if post.body != "" {
		out.WriteString(prefixLines(post.body, indent+"  ", "") + "\n")
	}
}

// htmlMarkdown converts an API's HTML fragment (a post's content) to Markdown.
func htmlMarkdown(fragment string) string {
	holder := &html.Node{Type: html.ElementNode, Data: divTag, DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(fragment), holder)
	if err != nil {
		obs.Logger(context.Background()).Warn("harvest: a post's HTML content could not be parsed; kept as text",
			obs.FieldErr, err.Error())
		return strings.TrimSpace(fragment)
	}
	for _, node := range nodes {
		holder.AppendChild(node)
	}
	return strings.Join(markdownRenderer{}.blocks(holder), "\n\n")
}
