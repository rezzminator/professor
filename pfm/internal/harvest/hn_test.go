package harvest

import (
	"context"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"golang.org/x/net/html"
)

// The fixture is a captured Hacker News item page (story 49806430), trimmed to
// two of its comment subtrees and scrubbed: usernames are placeholders, the
// page's auth and hmac tokens zeroed, its scripts dropped, and the stated
// count set to the comments the trim kept (53). It holds 55 comment rows:
// 53 with their text (six of them under a collapsed parent, class noshow) and
// two "[flagged]" placeholders HN keeps because they have replies.
const (
	hnStoryID        = "49806430"
	hnThreadPath     = "/item?id=" + hnStoryID
	hnThreadURL      = "https://news.ycombinator.com" + hnThreadPath
	hnFixtureStated  = 53
	hnFixtureFlagged = 2
)

// hnMoreLink is HN's "More" link as its listing pages serve it (captured from
// news.ycombinator.com/news), pointed at the thread's second page.
const hnMoreLink = `<table border="0"><tr class="morespace" style="height:10px"></tr><tr><td colspan="2"></td>` +
	`<td class='title'><a href='item?id=` + hnStoryID + `&amp;p=2' class='morelink' rel='next'>More</a></td></tr></table>`

var hnRowRe = regexp.MustCompile(
	`<tr class="athing comtr[^"]*" id="(\d+)"><td><table border="0"><tr><td class="ind" indent="(\d+)">`,
)

func hnFixture(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/hn/item.html")
	if err != nil {
		t.Fatalf("read the captured HN fixture: %v", err)
	}
	return string(raw)
}

// hnFixtureRows returns the fixture's comment ids and depths in page order.
func hnFixtureRows(page string) (ids []string, depths []int) {
	for _, match := range hnRowRe.FindAllStringSubmatch(page, -1) {
		depth, _ := strconv.Atoi(match[2])
		ids, depths = append(ids, match[1]), append(depths, depth)
	}
	return ids, depths
}

// hnSplit cuts the fixture into a thread's two pages at comment row at: the
// first holds the rows before it and a "More" link, the second the rest.
func hnSplit(page string, at int) (first, second string) {
	starts := hnRowRe.FindAllStringIndex(page, -1)
	lastEnd := strings.Index(page[starts[len(starts)-1][0]:], "</td></tr></table></td></tr>")
	rowsEnd := starts[len(starts)-1][0] + lastEnd + len("</td></tr></table></td></tr>")
	head, tail := page[:starts[0][0]], page[rowsEnd:]
	closeTree := strings.Index(tail, "</table>") + len("</table>")
	first = head + page[starts[0][0]:starts[at][0]] + tail[:closeTree] + hnMoreLink + tail[closeTree:]
	second = head + page[starts[at][0]:rowsEnd] + tail
	return first, second
}

// hnSite serves a thread's pages: pages[0] at the thread's address, pages[1]
// at its p=2.
type hnSite struct {
	mu       sync.Mutex
	pages    []string
	status   map[string]int
	requests []string
}

func (site *hnSite) roundTrip(request *http.Request) (*http.Response, error) {
	uri := request.URL.RequestURI()
	if request.URL.Path == "/item" {
		site.mu.Lock()
		site.requests = append(site.requests, request.Method+" "+uri)
		site.mu.Unlock()
	}
	if status := site.status[uri]; status != 0 {
		return response(request, status, "text/html", "<html><body>refused</body></html>"), nil
	}
	page := map[string]int{hnThreadPath: 0, hnThreadPath + "&p=2": 1}
	if index, ok := page[uri]; ok && index < len(site.pages) {
		return response(request, http.StatusOK, "text/html; charset=utf-8", site.pages[index]), nil
	}
	return response(request, http.StatusNotFound, "text/html", "<html><body>not found</body></html>"), nil
}

func (site *hnSite) harvester(t *testing.T) (*Harvester, *pacingClock) {
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

// hnCommentHeaderRe reads a rendered comment's heading: its indent and id.
var hnCommentHeaderRe = regexp.MustCompile(
	`(?m)^( *)- \*\*[^*]+\*\* · .*\[#(\d+)\]\(https://news\.ycombinator\.com/item\?id=\d+\)`,
)

// hnAssertTree checks content renders every fixture comment once, in page
// order, at its depth.
func hnAssertTree(t *testing.T, content string, ids []string, depths []int) {
	t.Helper()
	var gotIDs []string
	var gotDepths []int
	for _, match := range hnCommentHeaderRe.FindAllStringSubmatch(content, -1) {
		gotIDs, gotDepths = append(gotIDs, match[2]), append(gotDepths, len(match[1])/2)
	}
	if strings.Join(gotIDs, ",") != strings.Join(ids, ",") {
		t.Fatalf("comments or their order are wrong:\n got %d %v\nwant %d %v\n%.1500s",
			len(gotIDs), gotIDs, len(ids), ids, content)
	}
	for index := range depths {
		if gotDepths[index] != depths[index] {
			t.Fatalf("comment %s renders at depth %d, the page nests it at %d", ids[index], gotDepths[index],
				depths[index])
		}
	}
}

// TestHNThreadRendersEveryCommentAndReconciles: a thread whose whole tree is
// in its item page renders every comment once, in thread order at its depth,
// with its author and time; the two "[flagged]" placeholders are named apart
// from the stated count, which reconciles — the artifact is complete, and a
// second harvest is identical.
func TestHNThreadRendersEveryCommentAndReconciles(t *testing.T) {
	page := hnFixture(t)
	site := &hnSite{pages: []string{page}}
	h, pacing := site.harvester(t)
	result := h.FetchWithOptions(context.Background(), hnThreadURL, FetchOptions{Refresh: true})
	if result.Error != "" || result.Partial != "" {
		t.Fatalf(
			"the thread is not complete: method=%q partial=%q error=%q",
			result.Method,
			result.Partial,
			result.Error,
		)
	}
	ids, depths := hnFixtureRows(page)
	if len(ids) != hnFixtureStated+hnFixtureFlagged {
		t.Fatalf("the fixture holds %d rows, want %d", len(ids), hnFixtureStated+hnFixtureFlagged)
	}
	hnAssertTree(t, result.Content, ids, depths)
	for _, want := range []string{
		"# Pentagon says overreliance on AI contributed to missile strike on Iran school",
		"**Thread:** " + hnThreadURL,
		"**Link:** https://www.bloomberg.com/graphics/2026-iran-school-attack/",
		"**Posted:** 2026-09-22 19:03 UTC",
		"**Comments:** 53 stated · 53 loaded (2 flagged placeholder(s) without text, not counted)\n",
		"https://archive.ph/0V37g",
		"· [#49808036](https://news.ycombinator.com/item?id=49808036) · *flagged*",
	} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("the artifact lacks %q:\n%.2000s", want, result.Content)
		}
	}
	for _, unwanted := range []string{"[–]", "more]", "reply?id=", "add comment", "Guidelines"} {
		if strings.Contains(result.Content, unwanted) {
			t.Fatalf("the artifact carries page furniture %q", unwanted)
		}
	}
	if len(site.requests) != 1 || len(pacing.sleeps) != 0 {
		t.Fatalf("a one-page thread sent %v and paced %d times", site.requests, len(pacing.sleeps))
	}
	again := h.FetchWithOptions(context.Background(), hnThreadURL, FetchOptions{Refresh: true})
	if again.Content != result.Content {
		t.Fatal("a second harvest of the same thread differs")
	}
}

// TestHNMorePagesAreFollowedAndMerged: a thread split across two pages by a
// "More" link has its second page followed at the loader pace, its comments
// merged in after the first page's, and the count reconciles.
func TestHNMorePagesAreFollowedAndMerged(t *testing.T) {
	page := hnFixture(t)
	first, second := hnSplit(page, 30)
	site := &hnSite{pages: []string{first, second}}
	h, pacing := site.harvester(t)
	result := h.FetchWithOptions(context.Background(), hnThreadURL, FetchOptions{Refresh: true})
	if result.Error != "" || result.Partial != "" {
		t.Fatalf("the two-page thread is not complete: partial=%q error=%q", result.Partial, result.Error)
	}
	ids, depths := hnFixtureRows(page)
	hnAssertTree(t, result.Content, ids, depths)
	if !strings.Contains(result.Content, "**Comments:** 53 stated · 53 loaded (") {
		t.Fatalf("the count does not reconcile:\n%.800s", result.Content)
	}
	want := []string{"GET " + hnThreadPath, "GET " + hnThreadPath + "&p=2"}
	if strings.Join(site.requests, "\n") != strings.Join(want, "\n") || len(pacing.sleeps) != 1 {
		t.Fatalf("requests %v paced %d times, want %v paced once", site.requests, len(pacing.sleeps), want)
	}
}

// TestHNUnloadedCommentsFlagThePartial: whatever the page advertises and the
// artifact does not hold is named and flags it partial — a "More" page that
// failed, one answered by another item's page, one whose address does not
// parse, stated comments the page does not serve, and a count the subline
// does not state.
func TestHNUnloadedCommentsFlagThePartial(t *testing.T) {
	for _, tc := range []struct {
		name  string
		pages func(page string) []string
		// status answers the second page with an HTTP status.
		status int
		want   []string
	}{
		{
			"the second page refused",
			func(page string) []string { first, second := hnSplit(page, 30); return []string{first, second} },
			http.StatusInternalServerError,
			[]string{
				"hacker news thread: 30 of 53 comments loaded", "the comments on page 2 are not loaded",
				"\"More\" link (page 2)", "HTTP 500",
			},
		},
		{
			"the second page is another item's",
			func(page string) []string {
				first, second := hnSplit(page, 30)
				return []string{first, strings.ReplaceAll(second, `class="athing submission" id="`+hnStoryID,
					`class="athing submission" id="49800000`)}
			},
			0,
			[]string{"30 of 53 comments loaded", "answered by the page of another item"},
		},
		{
			"stated comments not served",
			func(page string) []string {
				return []string{strings.Replace(page, "53&nbsp;comments", "60&nbsp;comments", 1)}
			},
			0,
			[]string{"53 of 60 comments loaded", "7 stated comment(s) not in the page"},
		},
		{
			"a \"More\" link whose address does not parse",
			func(page string) []string {
				first, _ := hnSplit(page, 30)
				return []string{strings.Replace(first, `href='item?id=`, `href='%zz/item?id=`, 1)}
			},
			0,
			[]string{"30 of 53 comments loaded", "1 \"More\" link(s) whose address does not parse"},
		},
		{
			"no stated count",
			func(page string) []string {
				return []string{strings.Replace(page, "53&nbsp;comments", "", 1)}
			},
			0,
			[]string{"53 comments loaded, the stated count not read", "the stated comment count was not read"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			site := &hnSite{pages: tc.pages(hnFixture(t))}
			if tc.status != 0 {
				site.status = map[string]int{hnThreadPath + "&p=2": tc.status}
			}
			h, _ := site.harvester(t)
			result := h.FetchWithOptions(context.Background(), hnThreadURL, FetchOptions{Refresh: true})
			if result.Error != "" {
				t.Fatalf("fetch failed: %q", result.Error)
			}
			for _, want := range tc.want {
				if !strings.Contains(result.Partial, want) {
					t.Fatalf("the partial marker lacks %q: %q", want, result.Partial)
				}
			}
			if !strings.Contains(result.Content, "· gaps: ") {
				t.Fatalf("the count line names no gap:\n%.800s", result.Content)
			}
		})
	}
}

// TestHNNonThreadURLsTakeTheGenericPath: an HN page that is not a story's
// item page (here the fixture with its story row turned into a comment's, as
// a comment's own page carries it) is not claimed.
func TestHNNonThreadURLsTakeTheGenericPath(t *testing.T) {
	page := strings.Replace(hnFixture(t), `class="athing submission"`, `class="athing comtr"`, 1)
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatalf("parse the fixture: %v", err)
	}
	if _, name, ok := extractForSite(hnThreadURL, doc); ok {
		t.Fatalf("a page with no story row was claimed by %q", name)
	}
}
