package harvest

import (
	"context"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// The fixtures are captured from GitHub discussion vercel/next.js#41745 and
// its own fragment loaders, trimmed and scrubbed: usernames are placeholders,
// reactions, action menus, avatars and tracking attributes dropped. The page
// holds the post, comment 3963590 (one of its replies shown, the other behind
// its "Show 1 previous reply" form), the "hidden items" pagination form, and
// comment 3966674 with its reply; the pagination answer holds comments 3968436
// and 3969880 (hidden by a maintainer, its reply hidden too); the threads
// answer holds comment 3963590's two replies. The stated counts ("N comments ·
// N replies", "N replies" per comment, "N hidden items") are set to what the
// trim kept: 4 comments, 4 replies.
const (
	ghdPath      = "/vercel/next.js/discussions/41745"
	ghdURL       = "https://github.com" + ghdPath
	ghdPagesURI  = ghdPath + "/pages?after=DC_kwDOBC3Cis4APIbf&before=DC_kwDOBC3Cis4AV-_x"
	ghdThreadURI = ghdPath + "/comments/3963590/threads?anchor_id=3978810&back_page=1&forward_page=0"
)

// ghdOrder is every comment and reply of the fixtures in thread order: the
// hidden items splice in where their form stood, the replies under their
// comment.
var ghdOrder = []string{"3963590", "3973292", "3978810", "3968436", "3969880", "3972217", "3966674", "3986566"}

func ghdFixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/github/" + name)
	if err != nil {
		t.Fatalf("read the captured discussion fixture %s: %v", name, err)
	}
	return string(raw)
}

// ghdSite serves the discussion page and its two fragment loaders by request
// URI; status answers a URI with that HTTP status, body replaces an answer.
type ghdSite struct {
	mu       sync.Mutex
	answers  map[string]string
	status   map[string]int
	requests []string
}

func newGHDSite(t *testing.T) *ghdSite {
	t.Helper()
	return &ghdSite{answers: map[string]string{
		ghdPath:      ghdFixture(t, "discussion-page.html"),
		ghdPagesURI:  ghdFixture(t, "discussion-pages.html"),
		ghdThreadURI: ghdFixture(t, "discussion-threads.html"),
	}, status: map[string]int{}}
}

func (site *ghdSite) roundTrip(request *http.Request) (*http.Response, error) {
	uri := request.URL.RequestURI()
	if request.URL.Host == githubHost {
		site.mu.Lock()
		site.requests = append(site.requests, uri)
		site.mu.Unlock()
	}
	if status := site.status[uri]; status != 0 {
		return response(request, status, "text/html", "<html><body>refused</body></html>"), nil
	}
	if body, ok := site.answers[uri]; ok {
		return response(request, http.StatusOK, "text/html; charset=utf-8", body), nil
	}
	return response(request, http.StatusNotFound, "text/html", "<html><body>not found</body></html>"), nil
}

func (site *ghdSite) harvester(t *testing.T) (*Harvester, *pacingClock) {
	t.Helper()
	missing := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusNotFound, "application/json", `{}`), nil
	})
	pacing := newPacingClock()
	return mustNew(t, Options{
		CacheDir:    t.TempDir(),
		Client:      &http.Client{Transport: roundTripFunc(site.roundTrip)},
		Chrome:      &http.Client{Transport: roundTripFunc(site.roundTrip)},
		Jina:        &http.Client{Transport: missing},
		OA:          &http.Client{Transport: missing},
		Converter:   &browserSpyConverter{},
		BrowserRung: browserOff(),
		Clock:       pacing,
	}), pacing
}

var ghdEntryRe = regexp.MustCompile(`(?m)^( *)- \*\*[^*]+\*\* · .*\[#(\d+)\]\(` + regexp.QuoteMeta(ghdURL) +
	`#discussioncomment-\d+\)`)

// ghdEntries reads the rendered comments: their ids in order and their depths.
func ghdEntries(content string) (ids []string, depths []int) {
	for _, match := range ghdEntryRe.FindAllStringSubmatch(content, -1) {
		ids, depths = append(ids, match[2]), append(depths, len(match[1])/2)
	}
	return ids, depths
}

// TestGitHubDiscussionLoadsHiddenItemsAndReplies: a discussion whose middle
// sits behind its "hidden items" form and whose comment folds replies behind
// "Show 1 previous reply" has both followed at the loader pace, each answer
// spliced in its place, every comment and reply rendered once in thread order
// under its comment, and the stated counts reconcile — the artifact complete,
// a second harvest identical.
func TestGitHubDiscussionLoadsHiddenItemsAndReplies(t *testing.T) {
	site := newGHDSite(t)
	h, pacing := site.harvester(t)
	result := h.FetchWithOptions(context.Background(), ghdURL, FetchOptions{Refresh: true})
	if result.Error != "" || result.Partial != "" {
		t.Fatalf("the discussion is not complete: requests=%v method=%q partial=%q error=%q\n%.1500s", site.requests,
			result.Method, result.Partial, result.Error, result.Content)
	}
	ids, depths := ghdEntries(result.Content)
	if strings.Join(ids, ",") != strings.Join(ghdOrder, ",") {
		t.Fatalf("comments or their order are wrong:\n got %v\nwant %v\n%.3000s", ids, ghdOrder, result.Content)
	}
	if want := []int{0, 1, 1, 0, 0, 1, 0, 1}; !equalInts(depths, want) {
		t.Fatalf("depths %v, want %v", depths, want)
	}
	for _, want := range []string{
		"# [Feedback] App Directory Beta (#41745)",
		"**Author:** user-129 · **Opened:** 2022-10-24 19:50 UTC",
		"**Thread:** " + ghdURL,
		"**Comments:** 4 stated · 4 loaded · **Replies:** 4 stated · 4 loaded (2 hidden by a maintainer, " +
			"their text not shown)\n",
		"introduces the [`app` directory](https://beta.nextjs.org/docs/routing/fundamentals)",
		"- **user-140** · 2022-10-25 19:28 UTC · [#3963590](" + ghdURL + "#discussioncomment-3963590)",
		"[#3969880](" + ghdURL + "#discussioncomment-3969880) · *hidden by a maintainer*",
		"how to handle form POSTs",
	} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("the artifact lacks %q:\n%.3000s", want, result.Content)
		}
	}
	for _, unwanted := range []string{"hidden items", "Show 1 previous reply", "Load more", "View full answer"} {
		if strings.Contains(result.Content, unwanted) {
			t.Fatalf("the artifact carries page furniture %q", unwanted)
		}
	}
	want := []string{ghdPath, ghdThreadURI, ghdPagesURI}
	if strings.Join(site.requests, "\n") != strings.Join(want, "\n") || len(pacing.sleeps) != 2 {
		t.Fatalf("requests %v paced %d times, want %v paced twice", site.requests, len(pacing.sleeps), want)
	}
	again := h.FetchWithOptions(context.Background(), ghdURL, FetchOptions{Refresh: true})
	if again.Content != result.Content {
		t.Fatal("a second harvest of the same discussion differs")
	}
}

func equalInts(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
