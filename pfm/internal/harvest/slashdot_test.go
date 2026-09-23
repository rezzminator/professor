package harvest

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// The fixtures are a captured Slashdot story page and its comments_fetch
// answer (testdata/slashdot), trimmed and scrubbed: the story an invented one,
// the page cut to its story, comment bubbles, comment tree and D2 state; the
// tree cut to three comments with their markup (one anonymous) and one
// placeholder of a comment under the reader's threshold holding a reply; the
// answer cut to three comments — the placeholder's, a reply to a page comment
// and a top-level one — keeping the object-literal shape the site serves (bare
// keys, escaped markup strings). Nicks, uids, mail and journal addresses are
// placeholders.
const (
	slashdotURL      = "https://news.slashdot.org/story/26/09/22/0600001/an-example-story"
	slashdotPagePath = "news.slashdot.org/story/26/09/22/0600001/an-example-story"
	slashdotAjax     = "news.slashdot.org/ajax.pl"
)

func slashdotSite(t *testing.T) *socialSite {
	return &socialSite{apiPath: "/ajax.pl", answers: map[string]string{
		slashdotPagePath: socialFixture(t, "slashdot/story-page.html"),
		slashdotAjax:     socialFixture(t, "slashdot/comments-fetch.txt"),
	}}
}

// TestSlashdotStoryLoadsEveryComment: a Slashdot story renders its story, the
// comments its page holds, and the rest of its discussion read from the
// site's comments_fetch answer on the page's own host; every comment renders
// once, nested under the comment it answers (a page placeholder filled from
// the answer), in comment order; the stated total is the answer's own; the
// artifact is complete, and a second harvest is identical.
func TestSlashdotStoryLoadsEveryComment(t *testing.T) {
	site := slashdotSite(t)
	h := site.harvester(t)
	result := h.FetchWithOptions(context.Background(), slashdotURL, FetchOptions{Refresh: true})
	if result.Error != "" || result.Partial != "" {
		t.Fatalf("the story is not complete: partial=%q error=%q\n%.2500s", result.Partial, result.Error,
			result.Content)
	}
	want := "66000001,66000002,66000005,66000003,66000004,66000006"
	if got := strings.Join(socialRendered(result.Content), ","); got != want {
		t.Fatalf("comments or their order are wrong: got %v, want %v\n%.2500s", got, want, result.Content)
	}
	for _, text := range []string{
		"# An Example Story About Placeholder Systems",
		"**Author:** editor-1 · **Posted:** Tuesday September 22, 2026 @09:04AM",
		"**Post:** " + slashdotURL,
		"**Replies:** 6 stated · 6 loaded",
		"[a linked report](https://example.com/report)",
		"- **user-1** · Tuesday September 22, 2026 @11:44AM · [66000001]" +
			"(https://news.slashdot.org/comments.pl?sid=24000001&cid=66000001)",
		"**A first comment** (Score:5, Interesting)",
		"The first comment's text, with *emphasis*.",
		"\n  - **Anonymous Coward** · Tuesday September 22, 2026 @12:01PM · [66000002]",
		"\n    - **Anonymous Coward** · Tuesday September 22, 2026 @02:10PM · [66000005]",
		"A reply loaded after the page, with a [link](https://example.com/source).",
		"- **user-3** · Tuesday September 22, 2026 @12:30PM · [66000003]",
		"**A hidden comment** (Score:-1, Troll)",
		"\n  - **user-2** · Tuesday September 22, 2026 @01:15PM · [66000004]",
	} {
		if !strings.Contains(result.Content, text) {
			t.Fatalf("the artifact lacks %q:\n%.2500s", text, result.Content)
		}
	}
	if strings.Contains(result.Content, "1000001") || strings.Contains(result.Content, "user-3@example.com") {
		t.Fatalf("the artifact repeats a commenter's uid or mail address:\n%.2500s", result.Content)
	}
	if len(site.requests) != 1 {
		t.Fatalf("comments_fetch requests %v, want one", site.requests)
	}
	again := h.FetchWithOptions(context.Background(), slashdotURL, FetchOptions{Refresh: true})
	if again.Content != result.Content {
		t.Fatal("a second harvest of the same story differs")
	}
}

// TestSlashdotLoaderAsksForTheWholeDiscussion: the loader posts the page's
// discussion id and seen list to the page host's /ajax.pl, asking for every
// comment at the lowest threshold.
func TestSlashdotLoaderAsksForTheWholeDiscussion(t *testing.T) {
	doc, err := html.Parse(strings.NewReader(socialFixture(t, "slashdot/story-page.html")))
	if err != nil {
		t.Fatal(err)
	}
	page, err := url.Parse(slashdotURL)
	if err != nil {
		t.Fatal(err)
	}
	loaders := slashdotLoaders(doc, page)
	if len(loaders) != 1 {
		t.Fatalf("loaders %d, want one", len(loaders))
	}
	loader := loaders[0]
	want := url.Values{
		"op": {"comments_fetch"}, "discussion_id": {"24000001"}, "fetch_all": {"1"},
		"d2_seen": {"66000001,1,1,1"}, "threshold": {"-1"}, "highlightthresh": {"-1"},
	}
	if loader.method != http.MethodPost || loader.target != "https://news.slashdot.org/ajax.pl" ||
		loader.form.Encode() != want.Encode() {
		t.Fatalf("loader %s %s %s, want POST /ajax.pl %s", loader.method, loader.target, loader.form.Encode(),
			want.Encode())
	}
}

// TestSlashdotCommentsNotLoadedNameTheGap: an answer the site refuses, or one
// that is not this discussion's comment list, is never rendered: the story
// renders its page's comments with the rest named, stated · loaded.
func TestSlashdotCommentsNotLoadedNameTheGap(t *testing.T) {
	for name, mutate := range map[string]func(site *socialSite){
		"refused": func(site *socialSite) { site.status = map[string]int{slashdotAjax: http.StatusForbidden} },
		"another discussion": func(site *socialSite) {
			site.answers[slashdotAjax] = strings.ReplaceAll(site.answers[slashdotAjax], "sid=24000001", "sid=24000002")
		},
		"empty": func(site *socialSite) { site.answers[slashdotAjax] = "" },
	} {
		t.Run(name, func(t *testing.T) {
			site := slashdotSite(t)
			mutate(site)
			result := site.harvester(t).FetchWithOptions(context.Background(), slashdotURL,
				FetchOptions{Refresh: true})
			if !strings.Contains(result.Partial, "slashdot story: 3 of 6 stated replies loaded") ||
				!strings.Contains(result.Partial, "3 stated repl(ies) not served") {
				t.Fatalf("the partial marker does not name the unloaded comments: %q (error %q)\n%.1500s",
					result.Partial, result.Error, result.Content)
			}
			if got := strings.Join(socialRendered(result.Content), ","); got != "66000001,66000002,66000004" {
				t.Fatalf("rendered %v, want only the page's comments", got)
			}
		})
	}
}
