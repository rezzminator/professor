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

// The fixture is a captured Lobsters story page (lobste.rs/s/mroowi), trimmed
// to its last eleven top-level comment subtrees and scrubbed: usernames are
// placeholders (in their /~name links, avatar paths and mentions), the
// article's domain is example.org, the page's csrf tokens are zeroed, and the
// stated count is set to the comments the trim kept (30). It holds 33 comments:
// 30 with their text and three "[Comment removed by author]" placeholders,
// each comment with the avatar image and the vote-score link (/login) the
// site serves a signed-out reader.
const (
	lobstersStoryPath = "/s/mroowi"
	lobstersStoryURL  = "https://lobste.rs" + lobstersStoryPath
)

// lobstersFixtureDepths are the fixture's comments in page order with their
// depth, as the site's own API (/s/mroowi.json) nests them.
var lobstersFixtureDepths = []struct {
	id    string
	depth int
}{
	{"7gbnnu", 0},
	{"voankz", 0},
	{"jj3hxz", 1},
	{"lmzsgd", 2},
	{"8su7c7", 1},
	{"7ok0qo", 2},
	{"ke4wwa", 0},
	{"aivhdl", 1},
	{"ykxnsj", 0},
	{"mem8l4", 0},
	{"8dj53e", 1},
	{"vbgxgx", 0},
	{"vddjp1", 0},
	{"qzjrau", 1},
	{"ko8c3u", 1},
	{"n36opm", 0},
	{"ulqdpn", 0},
	{"uzemmo", 1},
	{"chzc6q", 1},
	{"7leood", 0},
	{"nttlhd", 0},
	{"vom03m", 0},
	{"johrbf", 0},
	{"fmzvmu", 0},
	{"uoal53", 0},
	{"aqpv1p", 0},
	{"hnpsta", 0},
	{"sewdto", 0},
	{"at0sug", 0},
	{"ewnu4l", 1},
	{"nu1agv", 0},
	{"ujw8by", 0},
	{"q5btpy", 0},
}

// lobstersSite serves the story page; every other address (an avatar, a
// login page) is not found.
type lobstersSite struct {
	mu       sync.Mutex
	page     string
	requests []string
}

func (site *lobstersSite) roundTrip(request *http.Request) (*http.Response, error) {
	site.mu.Lock()
	site.requests = append(site.requests, request.Method+" "+request.URL.RequestURI())
	site.mu.Unlock()
	if request.URL.RequestURI() == lobstersStoryPath {
		return response(request, http.StatusOK, "text/html; charset=utf-8", site.page), nil
	}
	return response(request, http.StatusNotFound, "text/html", "<html><body>not found</body></html>"), nil
}

// lobstersCommentHeaderRe reads a rendered comment's heading: its indent and
// id.
var lobstersCommentHeaderRe = regexp.MustCompile(
	`(?m)^( *)- \*\*[^*]+\*\* · .*\[#([a-z0-9]+)\]\(https://lobste\.rs/c/[a-z0-9]+\)`,
)

// TestLobstersStoryRendersCommentsWithoutAvatarsOrVoteLinks: a story page
// renders every comment once, in thread order at its depth, without the
// per-comment avatar images and vote-score links; the removed comments are
// named apart from the stated count, which reconciles — the artifact is
// complete (no image partial), and a second harvest is identical.
func TestLobstersStoryRendersCommentsWithoutAvatarsOrVoteLinks(t *testing.T) {
	raw, err := os.ReadFile("testdata/lobsters/story.html")
	if err != nil {
		t.Fatalf("read the captured Lobsters fixture: %v", err)
	}
	site := &lobstersSite{page: string(raw)}
	missing := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusNotFound, "application/json", `{}`), nil
	})
	h := mustNew(t, Options{
		CacheDir:    t.TempDir(),
		Client:      &http.Client{Transport: roundTripFunc(site.roundTrip)},
		Chrome:      &http.Client{Transport: roundTripFunc(site.roundTrip)},
		Jina:        &http.Client{Transport: missing},
		OA:          &http.Client{Transport: missing},
		Converter:   &browserSpyConverter{},
		BrowserRung: browserOff(),
		Clock:       newPacingClock(),
	})
	result := h.FetchWithOptions(context.Background(), lobstersStoryURL, FetchOptions{Refresh: true})
	if result.Error != "" || result.Partial != "" {
		t.Fatalf("the story is not complete: method=%q partial=%q error=%q\n%.1500s",
			result.Method, result.Partial, result.Error, result.Content)
	}
	var gotIDs, wantIDs []string
	var gotDepths []int
	for _, match := range lobstersCommentHeaderRe.FindAllStringSubmatch(result.Content, -1) {
		gotIDs, gotDepths = append(gotIDs, match[2]), append(gotDepths, len(match[1])/2)
	}
	for _, comment := range lobstersFixtureDepths {
		wantIDs = append(wantIDs, comment.id)
	}
	if strings.Join(gotIDs, ",") != strings.Join(wantIDs, ",") {
		t.Fatalf("comments or their order are wrong:\n got %d %v\nwant %d %v\n%.1500s",
			len(gotIDs), gotIDs, len(wantIDs), wantIDs, result.Content)
	}
	for index, comment := range lobstersFixtureDepths {
		if gotDepths[index] != comment.depth {
			t.Fatalf("comment %s renders at depth %d, the site nests it at %d", comment.id, gotDepths[index],
				comment.depth)
		}
	}
	for _, want := range []string{
		"# Being kicked out of the tech industry",
		"**Thread:** " + lobstersStoryURL,
		"**Link:** https://www.example.org/essays/2026/kicked-out/",
		"**Author:** user1",
		"**Comments:** 30 stated · 30 loaded (3 comment(s) removed, placeholders without text, not counted)\n",
		"[@user2](https://lobste.rs/~user2), i feel for ya!",
		"· [#vom03m](https://lobste.rs/c/vom03m) · *Comment removed by author*",
	} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("the artifact lacks %q:\n%.2000s", want, result.Content)
		}
	}
	for _, unwanted := range []string{"/avatars/", "![", "](/login)", "/login", "Login", "caches"} {
		if strings.Contains(result.Content, unwanted) {
			t.Fatalf("the artifact carries page furniture %q:\n%.2000s", unwanted, result.Content)
		}
	}
	again := h.FetchWithOptions(context.Background(), lobstersStoryURL, FetchOptions{Refresh: true})
	if again.Content != result.Content {
		t.Fatal("a second harvest of the same story differs")
	}
}
