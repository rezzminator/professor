package harvest

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The fixtures are captured dev.to answers (testdata/devto), trimmed and
// scrubbed: usernames placeholders, bodies placeholder HTML of the captured
// shape, the comment tree cut to its first three threads (two children a
// level, three levels deep) plus the thread holding the captured deleted
// comment (its body "[deleted]", its user empty), and comments_count
// recomputed to the comments kept that are not deleted — the API's count
// leaves a deleted comment out. article-page.html keeps the captured page's
// metas and its data-article-id body.
const (
	devtoPath        = "/user-0/an-article-4g9c"
	devtoURL         = "https://dev.to" + devtoPath
	devtoArticleAPI  = "dev.to/api/articles/3759724"
	devtoCommentsAPI = "dev.to/api/comments?a_id=3759724"
)

func devtoSite(t *testing.T) *socialSite {
	return &socialSite{answers: map[string]string{
		"dev.to" + devtoPath: socialFixture(t, "devto/article-page.html"),
		devtoArticleAPI:      socialFixture(t, "devto/article.json"),
		devtoCommentsAPI:     socialFixture(t, "devto/comments.json"),
	}}
}

type devtoFixtureComment struct {
	ID       string                `json:"id_code"`
	Body     string                `json:"body_html"`
	Children []devtoFixtureComment `json:"children"`
}

// devtoWant is every comment id the fixture tree holds that is not deleted.
func devtoWant(t *testing.T) []string {
	t.Helper()
	var tree []devtoFixtureComment
	if err := json.Unmarshal([]byte(socialFixture(t, "devto/comments.json")), &tree); err != nil {
		t.Fatal(err)
	}
	var ids []string
	var walk func([]devtoFixtureComment)
	walk = func(comments []devtoFixtureComment) {
		for _, comment := range comments {
			if !strings.Contains(comment.Body, "[deleted]") {
				ids = append(ids, comment.ID)
			}
			walk(comment.Children)
		}
	}
	walk(tree)
	return ids
}

var socialEntryRe = regexp.MustCompile(`(?m)^ *- \*\*[^*]+\*\* · [^·]+ · \[([^\]]+)\]`)

func socialRendered(content string) []string {
	var ids []string
	for _, match := range socialEntryRe.FindAllStringSubmatch(content, -1) {
		ids = append(ids, match[1])
	}
	return ids
}

// TestDevtoArticleLoadsEveryComment: a dev.to article's comments are read
// from dev.to's API — the article's record, then its whole comment tree —
// sending no credential; every comment renders once in the API's tree order,
// a reply nested under its parent, a deleted comment named as such and kept
// out of the count the API states; the artifact is complete, and a second
// harvest is identical.
func TestDevtoArticleLoadsEveryComment(t *testing.T) {
	site := devtoSite(t)
	h := site.harvester(t)
	result := h.FetchWithOptions(context.Background(), devtoURL, FetchOptions{Refresh: true})
	if result.Error != "" || result.Partial != "" {
		t.Fatalf("the article is not complete: partial=%q error=%q\n%.1500s", result.Partial, result.Error,
			result.Content)
	}
	want := devtoWant(t)
	if got := socialRendered(result.Content); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("comments or their order are wrong: got %v, want %v\n%.2500s", got, want, result.Content)
	}
	for _, text := range []string{
		"# An article title",
		"**Post:** " + devtoURL,
		"**Replies:** 12 stated · 12 loaded · 1 deleted (not in the stated count)",
		"The article body, with **bold**.",
		"\n  - **@user-", // a reply to a comment, nested
		"*a reply deleted*",
		"with *emphasis* and a [link](https://example.com/).",
	} {
		if !strings.Contains(result.Content, text) {
			t.Fatalf("the artifact lacks %q:\n%.2500s", text, result.Content)
		}
	}
	if strings.Join(site.requests, "\n") != devtoArticleAPI+"\n"+devtoCommentsAPI {
		t.Fatalf("API requests %v", site.requests)
	}
	for _, header := range site.headers {
		if header.Get("Authorization") != "" || header.Get("Cookie") != "" || header.Get("Api-Key") != "" {
			t.Fatalf("an API request carried a credential: %v", header)
		}
	}
	again := h.FetchWithOptions(context.Background(), devtoURL, FetchOptions{Refresh: true})
	if again.Content != result.Content {
		t.Fatal("a second harvest of the same article differs")
	}
}

// TestDevtoUnservedCommentsFlagThePartial: an article stating more comments
// than its tree serves renders what was served and names the rest.
func TestDevtoUnservedCommentsFlagThePartial(t *testing.T) {
	site := devtoSite(t)
	site.answers[devtoArticleAPI] = strings.Replace(site.answers[devtoArticleAPI],
		`"comments_count": 12`, `"comments_count": 20`, 1)
	result := site.harvester(t).FetchWithOptions(context.Background(), devtoURL, FetchOptions{Refresh: true})
	got := socialRendered(result.Content)
	sort.Strings(got)
	if result.Error != "" || len(got) != 12 {
		t.Fatalf("the served comments were not rendered: error=%q\n%.1500s", result.Error, result.Content)
	}
	for _, want := range []string{"dev.to article: 12 of 20 stated replies loaded", "8 stated repl(ies) not served"} {
		if !strings.Contains(result.Partial, want) {
			t.Fatalf("the partial marker lacks %q: %q", want, result.Partial)
		}
	}
}

// TestDevtoRecordNotLoadedServesThePage: an article whose record the API
// would not answer is not claimed; the page goes the generic path, the gap
// named, and its comments are never requested.
func TestDevtoRecordNotLoadedServesThePage(t *testing.T) {
	site := devtoSite(t)
	site.status = map[string]int{devtoArticleAPI: http.StatusNotFound}
	result := site.servingHarvester(t).FetchWithOptions(context.Background(), devtoURL, FetchOptions{Refresh: true})
	if !strings.Contains(result.Partial, "the article's API record was not loaded") {
		t.Fatalf("the partial marker does not name the record: %q (error %q)", result.Partial, result.Error)
	}
	if strings.Join(site.requests, ",") != devtoArticleAPI {
		t.Fatalf("API requests %v, want the record alone", site.requests)
	}
}
