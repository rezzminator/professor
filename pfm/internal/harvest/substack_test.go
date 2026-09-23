package harvest

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// The fixtures are a captured Substack post page and its comments answer
// (testdata/substack), trimmed and scrubbed: the publication moved to an
// invented custom domain (so only the markup can name it Substack), names and
// handles placeholders, bodies placeholder text, the page cut to its
// substackcdn.com assets and its window._preloads script holding the captured
// post keys, and the comment tree cut to seven published comments plus the
// captured unavailable shapes: a deleted comment (body null, deleted true), a
// blocked one and one a moderator removed, each with its status; the post's
// comment_count is the published comments alone, as the API states it.
const (
	substackURL         = "https://www.example-pub.com/p/a-post"
	substackCommentsAPI = "www.example-pub.com/api/v1/post/214878061/comments?all_comments=true&sort=best_first"
)

func substackSite(t *testing.T) *socialSite {
	return &socialSite{answers: map[string]string{
		"www.example-pub.com/p/a-post": socialFixture(t, "substack/post-page.html"),
		substackCommentsAPI:            socialFixture(t, "substack/comments.json"),
	}}
}

var substackWant = []string{
	"337312099", "337314335", "337321477", "337313520", "337316944", "337318085", "337319266",
}

// TestSubstackPostLoadsEveryComment: a Substack post on a custom domain is
// known by its markup; its comments are read from the publication's API on the
// page's own host, sending no credential; every comment renders once in the
// API's tree order, a reply nested under its parent, the deleted, blocked and
// removed comments named apart from the stated count; the artifact is
// complete, and a second harvest is identical.
func TestSubstackPostLoadsEveryComment(t *testing.T) {
	site := substackSite(t)
	h := site.harvester(t)
	result := h.FetchWithOptions(context.Background(), substackURL, FetchOptions{Refresh: true})
	if result.Error != "" || result.Partial != "" {
		t.Fatalf("the post is not complete: partial=%q error=%q\n%.1500s", result.Partial, result.Error,
			result.Content)
	}
	if got := socialRendered(result.Content); strings.Join(got, ",") != strings.Join(substackWant, ",") {
		t.Fatalf("comments or their order are wrong: got %v, want %v\n%.2500s", got, substackWant, result.Content)
	}
	for _, text := range []string{
		"# A post title",
		"**Author:** @author-0",
		"**Post:** " + substackURL,
		"**Replies:** 7 stated · 7 loaded · 1 deleted (not in the stated count) · 1 blocked (not in the stated " +
			"count) · 1 removed by a moderator (not in the stated count)",
		"The post body, with **bold** and a [link](https://example.com/).",
		"[337314335](" + substackURL + "/comment/337314335)",
		"\n      - **@user-2**", // a reply under a deleted reply, nested
		"*a reply deleted*",
		"  > A quoted line of the post.",
	} {
		if !strings.Contains(result.Content, text) {
			t.Fatalf("the artifact lacks %q:\n%.2500s", text, result.Content)
		}
	}
	if strings.Join(site.requests, "\n") != substackCommentsAPI {
		t.Fatalf("API requests %v", site.requests)
	}
	for _, header := range site.headers {
		if header.Get("Authorization") != "" || header.Get("Cookie") != "" {
			t.Fatalf("an API request carried a credential: %v", header)
		}
	}
	again := h.FetchWithOptions(context.Background(), substackURL, FetchOptions{Refresh: true})
	if again.Content != result.Content {
		t.Fatal("a second harvest of the same post differs")
	}
}

// TestSubstackUnservedCommentsFlagThePartial: a post stating more comments
// than its tree serves renders what was served and names the rest.
func TestSubstackUnservedCommentsFlagThePartial(t *testing.T) {
	site := substackSite(t)
	site.answers["www.example-pub.com/p/a-post"] = strings.Replace(site.answers["www.example-pub.com/p/a-post"],
		`\"comment_count\":7`, `\"comment_count\":400`, 1)
	result := site.harvester(t).FetchWithOptions(context.Background(), substackURL, FetchOptions{Refresh: true})
	if result.Error != "" || len(socialRendered(result.Content)) != 7 {
		t.Fatalf("the served comments were not rendered: error=%q\n%.1500s", result.Error, result.Content)
	}
	for _, want := range []string{"substack post: 7 of 400 stated replies loaded", "393 stated repl(ies) not served"} {
		if !strings.Contains(result.Partial, want) {
			t.Fatalf("the partial marker lacks %q: %q", want, result.Partial)
		}
	}
}

// TestSubstackCommentsNotLoadedNameTheGap: a comment tree the API refuses, or
// one of another post, is never rendered: the post renders with its stated
// comments named unread, never as a post with no comments.
func TestSubstackCommentsNotLoadedNameTheGap(t *testing.T) {
	for name, mutate := range map[string]func(site *socialSite){
		"refused": func(site *socialSite) { site.status = map[string]int{substackCommentsAPI: http.StatusNotFound} },
		"another post's": func(site *socialSite) {
			site.answers[substackCommentsAPI] = strings.ReplaceAll(site.answers[substackCommentsAPI],
				`"post_id":214878061`, `"post_id":1`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			site := substackSite(t)
			mutate(site)
			result := site.servingHarvester(t).FetchWithOptions(context.Background(), substackURL,
				FetchOptions{Refresh: true})
			if !strings.Contains(result.Partial, "7 stated repl(ies) not read") {
				t.Fatalf("the partial marker does not name the unread comments: %q (error %q)\n%.1500s",
					result.Partial, result.Error, result.Content)
			}
			if got := socialRendered(result.Content); len(got) != 0 {
				t.Fatalf("comments rendered from an answer not proved this post's: %v", got)
			}
		})
	}
}
