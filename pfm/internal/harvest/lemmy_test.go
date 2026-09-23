package harvest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The fixtures are captured Lemmy answers (testdata/lemmy), trimmed and
// scrubbed: usernames placeholders (actor ids /u/<name>), contents
// placeholder Markdown, the comment list's first page kept whole (50
// comments, oldest first) and its second cut to its first five — a short page
// that ends the list; oldest first, every kept comment's parent is kept — and
// counts.comments recomputed to the comments kept. challenge.html is the
// captured wall lemmy.world serves a non-browser in the post page's place;
// post-page.html the captured lemmy-ui shell (its window.isoData script cut
// to its path) another instance serves.
const (
	lemmyPostID = "51036219"
	lemmyPath   = "/post/" + lemmyPostID
)

func lemmyCommentsKey(host string, page int) string {
	return fmt.Sprintf("%s/api/v3/comment/list?post_id=%s&type_=All&sort=Old&limit=50&page=%d", host, lemmyPostID, page)
}

func lemmySite(t *testing.T, host, pageFixture string) *socialSite {
	return &socialSite{answers: map[string]string{
		host + lemmyPath:                        socialFixture(t, pageFixture),
		host + "/api/v3/post?id=" + lemmyPostID: socialFixture(t, "lemmy/post.json"),
		lemmyCommentsKey(host, 1):               socialFixture(t, "lemmy/comments-1.json"),
		lemmyCommentsKey(host, 2):               socialFixture(t, "lemmy/comments-2.json"),
	}}
}

type lemmyFixturePage struct {
	Comments []struct {
		Comment struct {
			ID   int64  `json:"id"`
			Path string `json:"path"`
		} `json:"comment"`
	} `json:"comments"`
}

func lemmyWant(t *testing.T) []string {
	t.Helper()
	var ids []string
	for _, name := range []string{"lemmy/comments-1.json", "lemmy/comments-2.json"} {
		var page lemmyFixturePage
		if err := json.Unmarshal([]byte(socialFixture(t, name)), &page); err != nil {
			t.Fatal(err)
		}
		for _, entry := range page.Comments {
			ids = append(ids, strconv.FormatInt(entry.Comment.ID, 10))
		}
	}
	sort.Strings(ids)
	return ids
}

// TestLemmyPostLoadsEveryComment: a Lemmy post's comments are read from the
// instance's API — the post's record, then the comment list page after page
// until a short page ends it — sending no credential, even where the
// instance served a challenge wall in the page's place (lemmy.world, known by
// its host) or on another instance known by its markup; every comment
// renders once, a reply nested under its parent; the counts reconcile, the
// artifact is complete, and a second harvest is identical.
func TestLemmyPostLoadsEveryComment(t *testing.T) {
	for host, page := range map[string]string{
		"lemmy.world": "lemmy/challenge.html", "lemmy.example": "lemmy/post-page.html",
	} {
		site := lemmySite(t, host, page)
		if host == "lemmy.world" {
			site.status = map[string]int{host + lemmyPath: http.StatusForbidden}
		}
		h := site.harvester(t)
		result := h.FetchWithOptions(context.Background(), "https://"+host+lemmyPath, FetchOptions{Refresh: true})
		if result.Error != "" || result.Partial != "" {
			t.Fatalf("%s: the post is not complete: partial=%q error=%q\n%.1500s", host, result.Partial,
				result.Error, result.Content)
		}
		got := socialRendered(result.Content)
		sort.Strings(got)
		if want := lemmyWant(t); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("%s: comments are wrong: got %d %v, want %d", host, len(got), got, len(want))
		}
		for _, text := range []string{
			"# A post title",
			"**Replies:** 55 stated · 55 loaded",
			"The post body, with **bold**.",
			"\n  - **@user-", // a reply to a comment, nested
			"with *emphasis*.",
		} {
			if !strings.Contains(result.Content, text) {
				t.Fatalf("%s: the artifact lacks %q:\n%.2500s", host, text, result.Content)
			}
		}
		if len(site.requests) != 3 {
			t.Fatalf("%s: API requests %v, want the record and two comment pages", host, site.requests)
		}
		for _, header := range site.headers {
			if header.Get("Authorization") != "" || header.Get("Cookie") != "" {
				t.Fatalf("an API request carried a credential: %v", header)
			}
		}
		again := h.FetchWithOptions(context.Background(), "https://"+host+lemmyPath, FetchOptions{Refresh: true})
		if again.Content != result.Content {
			t.Fatalf("%s: a second harvest of the same post differs", host)
		}
	}
}

// TestLemmyDeletedCommentIsNamed: a comment its author deleted (the captured
// shape: content empty, deleted true) renders as a mark its replies still
// nest under, named apart from the stated count, which leaves it out.
func TestLemmyDeletedCommentIsNamed(t *testing.T) {
	site := lemmySite(t, "lemmy.example", "lemmy/post-page.html")
	var page lemmyFixturePage
	if err := json.Unmarshal([]byte(site.answers[lemmyCommentsKey("lemmy.example", 1)]), &page); err != nil {
		t.Fatal(err)
	}
	parent := page.Comments[0].Comment
	id := strconv.FormatInt(parent.ID, 10)
	body := site.answers[lemmyCommentsKey("lemmy.example", 1)]
	start := strings.Index(body, `"id": `+id)
	content := start + strings.Index(body[start:], `"content": "`)
	end := content + len(`"content": "`) + strings.Index(body[content+len(`"content": "`):], `"`)
	body = body[:content] + `"content": "` + body[end:]
	body = strings.Replace(body, `"deleted": false`, `"deleted": true`, 1)
	site.answers[lemmyCommentsKey("lemmy.example", 1)] = body
	site.answers["lemmy.example/api/v3/post?id="+lemmyPostID] = strings.Replace(
		site.answers["lemmy.example/api/v3/post?id="+lemmyPostID], `"comments": 55`, `"comments": 54`, 1)
	result := site.harvester(t).FetchWithOptions(context.Background(), "https://lemmy.example"+lemmyPath,
		FetchOptions{Refresh: true})
	if result.Error != "" || result.Partial != "" {
		t.Fatalf("the post is not complete: partial=%q error=%q", result.Partial, result.Error)
	}
	for _, text := range []string{
		"**Replies:** 54 stated · 54 loaded",
		"1 deleted (not in the stated count)",
		"- *a reply deleted* · ",
	} {
		if !strings.Contains(result.Content, text) {
			t.Fatalf("the artifact lacks %q:\n%.2500s", text, result.Content)
		}
	}
}

// TestLemmyUnreadPageFlagsThePartial: a comment page the API would not answer
// is named, and the comments it held count as not loaded.
func TestLemmyUnreadPageFlagsThePartial(t *testing.T) {
	site := lemmySite(t, "lemmy.example", "lemmy/post-page.html")
	site.status = map[string]int{lemmyCommentsKey("lemmy.example", 2): http.StatusInternalServerError}
	result := site.harvester(t).FetchWithOptions(context.Background(), "https://lemmy.example"+lemmyPath,
		FetchOptions{Refresh: true})
	for _, want := range []string{"lemmy post: 50 of 55 stated replies loaded", "comments page 2 not loaded"} {
		if !strings.Contains(result.Partial, want) {
			t.Fatalf("the partial marker lacks %q: %q (error %q)", want, result.Partial, result.Error)
		}
	}
}
