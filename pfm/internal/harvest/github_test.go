package harvest

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"golang.org/x/net/html"
)

// The fixtures are captured GitHub answers, trimmed and scrubbed: the
// repositories renamed (example-org/…), usernames placeholders, bodies
// replaced by placeholder Markdown, each object cut to the fields the
// extractor reads plus a few beside them. issue-page.html and pull-page.html
// keep the captured pages' route and og: metas. issue*.json is an issue
// stating 170 comments, listed over two pages (100 + 70); pull*.json a pull
// request stating 51 comments and 7 review comments, with 18 reviews (4 with
// a body, 7 empty "commented" ones), two of its comments minimized.
const (
	ghIssueNumber = "76920"
	ghIssuePath   = "/example-org/example-lang/issues/" + ghIssueNumber
	ghIssueURL    = "https://github.com" + ghIssuePath
	ghIssueAPI    = "/repos/example-org/example-lang/issues/" + ghIssueNumber
	ghPullNumber  = "54857"
	ghPullPath    = "/example-org/example-runtime/pull/" + ghPullNumber
	ghPullURL     = "https://github.com" + ghPullPath
	ghPullAPI     = "/repos/example-org/example-runtime"
)

// ghRateLimited is the API's rate-limit answer, as GitHub's REST
// documentation gives it (an address from TEST-NET-3 in the message).
const ghRateLimited = `{"message":"API rate limit exceeded for 203.0.113.7. (But here's the good news: ` +
	`Authenticated requests get a higher rate limit. Check out the documentation for more details.)",` +
	`"documentation_url":"https://docs.github.com/rest/overview/resources-in-the-rest-api#rate-limiting"}`

func ghFixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/github/" + name)
	if err != nil {
		t.Fatalf("read the captured GitHub fixture %s: %v", name, err)
	}
	return string(raw)
}

// ghSite serves a thread's page on github.com and its API answers on
// api.github.com, by request URI.
type ghSite struct {
	mu       sync.Mutex
	pages    map[string]string
	api      map[string]string
	status   map[string]int
	bodies   map[string]string
	requests []string
	headers  []http.Header
}

func (site *ghSite) roundTrip(request *http.Request) (*http.Response, error) {
	uri := request.URL.RequestURI()
	if request.URL.Host == "api.github.com" {
		site.mu.Lock()
		site.requests = append(site.requests, uri)
		site.headers = append(site.headers, request.Header.Clone())
		site.mu.Unlock()
		if status := site.status[uri]; status != 0 {
			return response(request, status, "application/json; charset=utf-8", site.bodies[uri]), nil
		}
		if body, ok := site.api[uri]; ok {
			return response(request, http.StatusOK, "application/json; charset=utf-8", body), nil
		}
		return response(request, http.StatusNotFound, "application/json", `{"message":"Not Found"}`), nil
	}
	if page, ok := site.pages[request.URL.Path]; ok {
		return response(request, http.StatusOK, "text/html; charset=utf-8", page), nil
	}
	return response(request, http.StatusNotFound, "text/html", "<html><body>not found</body></html>"), nil
}

func (site *ghSite) harvester(t *testing.T) (*Harvester, *pacingClock) {
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

func ghIssueSite(t *testing.T) *ghSite {
	return &ghSite{
		pages: map[string]string{ghIssuePath: ghFixture(t, "issue-page.html")},
		api: map[string]string{
			ghIssueAPI: ghFixture(t, "issue.json"),
			ghIssueAPI + "/comments?per_page=100&page=1": ghFixture(t, "issue-comments-1.json"),
			ghIssueAPI + "/comments?per_page=100&page=2": ghFixture(t, "issue-comments-2.json"),
		},
	}
}

func ghPullSite(t *testing.T) *ghSite {
	return &ghSite{
		pages: map[string]string{ghPullPath: ghFixture(t, "pull-page.html")},
		api: map[string]string{
			ghPullAPI + "/issues/" + ghPullNumber: ghFixture(t, "pull-issue.json"),
			ghPullAPI + "/issues/" + ghPullNumber + "/comments?per_page=100&page=1": ghFixture(
				t,
				"pull-comments-1.json",
			),
			ghPullAPI + "/pulls/" + ghPullNumber: ghFixture(t, "pull.json"),
			ghPullAPI + "/pulls/" + ghPullNumber + "/comments?per_page=100&page=1": ghFixture(
				t,
				"pull-review-comments-1.json",
			),
			ghPullAPI + "/pulls/" + ghPullNumber + "/reviews?per_page=100&page=1": ghFixture(
				t,
				"pull-reviews-1.json",
			),
		},
	}
}

// ghIDs reads the ids of a fixture list, in its order, with the entries
// selected by keep (all when nil).
func ghIDs(t *testing.T, raw string, keep func(entry map[string]any) bool) []string {
	t.Helper()
	var entries []map[string]any
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		t.Fatalf("decode a fixture list: %v", err)
	}
	var ids []string
	for _, entry := range entries {
		if keep == nil || keep(entry) {
			ids = append(ids, strconv.FormatInt(int64(entry["id"].(float64)), 10))
		}
	}
	return ids
}

var ghEntryRe = regexp.MustCompile(`(?m)^- \*\*[^*]+\*\* · [^\n]*\[#(\d+)\]\(https://github\.com/`)

// ghRendered returns the entry ids content renders, in order.
func ghRendered(content string) []string {
	var ids []string
	for _, match := range ghEntryRe.FindAllStringSubmatch(content, -1) {
		ids = append(ids, match[1])
	}
	return ids
}

// TestGitHubIssueLoadsEveryCommentAndReconciles: an issue whose page carries
// a slice of its comments is read from the API — its record, then both
// comment pages at the loader pace, sending no credential — and renders every
// comment once in order with its author and time; the count reconciles, the
// artifact is complete, and a second harvest is identical.
func TestGitHubIssueLoadsEveryCommentAndReconciles(t *testing.T) {
	site := ghIssueSite(t)
	h, pacing := site.harvester(t)
	result := h.FetchWithOptions(context.Background(), ghIssueURL, FetchOptions{Refresh: true})
	if result.Error != "" || result.Partial != "" {
		t.Fatalf(
			"the issue is not complete: method=%q partial=%q error=%q",
			result.Method,
			result.Partial,
			result.Error,
		)
	}
	want := append(ghIDs(t, ghFixture(t, "issue-comments-1.json"), nil),
		ghIDs(t, ghFixture(t, "issue-comments-2.json"), nil)...)
	if got := ghRendered(result.Content); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("comments or their order are wrong: got %d, want %d\n%.1500s", len(got), len(want), result.Content)
	}
	for _, text := range []string{
		"# Example issue (#76920)",
		"**Author:** user-1 · **Opened:** ",
		"**Thread:** " + ghIssueURL,
		"**Comments:** 170 stated · 170 loaded\n",
		"  ```go\n  fmt.Println(\"x\")\n  ```",
	} {
		if !strings.Contains(result.Content, text) {
			t.Fatalf("the artifact lacks %q:\n%.1500s", text, result.Content)
		}
	}
	wantRequests := []string{
		ghIssueAPI, ghIssueAPI + "/comments?per_page=100&page=1",
		ghIssueAPI + "/comments?per_page=100&page=2",
	}
	if strings.Join(site.requests, "\n") != strings.Join(wantRequests, "\n") || len(pacing.sleeps) != 3 {
		t.Fatalf(
			"API requests %v paced %d times, want %v paced 3 times",
			site.requests,
			len(pacing.sleeps),
			wantRequests,
		)
	}
	for _, header := range site.headers {
		if header.Get("Authorization") != "" || header.Get("Cookie") != "" {
			t.Fatalf("an API request carried a credential: %v", header)
		}
		if header.Get("Accept") != "application/vnd.github+json" {
			t.Fatalf("an API request asked for %q, not the API's JSON", header.Get("Accept"))
		}
	}
	again := h.FetchWithOptions(context.Background(), ghIssueURL, FetchOptions{Refresh: true})
	if again.Content != result.Content {
		t.Fatal("a second harvest of the same issue differs")
	}
}

// TestGitHubPullRequestLoadsCommentsReviewCommentsAndReviews: a pull request
// renders its comments, its review comments on diff lines and its reviews
// with a body or a verdict, merged in time order; both stated counts
// reconcile and the reviews are read to the end of their list.
func TestGitHubPullRequestLoadsCommentsReviewCommentsAndReviews(t *testing.T) {
	site := ghPullSite(t)
	h, _ := site.harvester(t)
	result := h.FetchWithOptions(context.Background(), ghPullURL, FetchOptions{Refresh: true})
	if result.Error != "" || result.Partial != "" {
		t.Fatalf("the pull request is not complete: partial=%q error=%q", result.Partial, result.Error)
	}
	comments := ghIDs(t, ghFixture(t, "pull-comments-1.json"), nil)
	reviewComments := ghIDs(t, ghFixture(t, "pull-review-comments-1.json"), nil)
	reviews := ghIDs(t, ghFixture(t, "pull-reviews-1.json"), func(entry map[string]any) bool {
		return entry["body"] != "" || entry["state"] != "COMMENTED"
	})
	got := map[string]int{}
	for _, id := range ghRendered(result.Content) {
		got[id]++
	}
	all := append(append(append([]string{}, comments...), reviewComments...), reviews...)
	for _, id := range all {
		if got[id] != 1 {
			t.Fatalf("entry %s renders %d times, want once:\n%.1500s", id, got[id], result.Content)
		}
	}
	if len(got) != len(all) {
		t.Fatalf("%d entries render, want %d", len(got), len(all))
	}
	for _, text := range []string{
		"**Comments:** 51 stated · 51 loaded · **Review comments:** 7 stated · 7 loaded · " +
			"**Reviews:** 18 loaded, the list read to its end (the API states no review count; 7 empty",
		"· review comment on `lib/_http_client.js` line 948 · reply to #2378223246 · [#2378224819]",
		"· review: changes requested · [#",
		"· *hidden by a maintainer (outdated)*",
	} {
		if !strings.Contains(result.Content, text) {
			t.Fatalf("the artifact lacks %q:\n%.3000s", text, result.Content)
		}
	}
	var stamps []string
	for _, line := range strings.Split(result.Content, "\n") {
		if strings.HasPrefix(line, "- **") {
			stamps = append(stamps, strings.SplitN(line, " · ", 3)[1])
		}
	}
	for index := 1; index < len(stamps); index++ {
		if stamps[index] < stamps[index-1] {
			t.Fatalf("the conversation is not in time order at %d: %s after %s", index, stamps[index], stamps[index-1])
		}
	}
}

// TestGitHubUnloadedCommentsFlagThePartial: whatever the thread states and
// the artifact does not hold is named and flags it partial — a comment page
// refused, the API's rate limit (which ends the following, never retried,
// and repeats nothing of the API's message), stated comments the API did not
// list, a page of another thread's comments, and a record never loaded.
func TestGitHubUnloadedCommentsFlagThePartial(t *testing.T) {
	page2 := ghIssueAPI + "/comments?per_page=100&page=2"
	for _, tc := range []struct {
		name   string
		edit   func(site *ghSite)
		want   []string
		unsent string
	}{
		{
			"a comment page refused",
			func(site *ghSite) { site.status = map[string]int{page2: http.StatusInternalServerError} },
			[]string{
				"github issue: 100 of 170 comments loaded", "comment page 2 of 2 (comment 101–170) not loaded",
				"HTTP 500",
			},
			"",
		},
		{
			"the rate limit",
			func(site *ghSite) {
				site.status = map[string]int{page2: http.StatusForbidden}
				site.bodies = map[string]string{page2: ghRateLimited}
			},
			[]string{
				"100 of 170 comments loaded", "comment page 2 of 2 (comment 101–170) not loaded",
				"the site answered HTTP 403 (rate limit exhausted) after 3 request(s); not retried",
			},
			"",
		},
		{
			"the rate limit before the record",
			func(site *ghSite) {
				site.status = map[string]int{ghIssueAPI: http.StatusForbidden}
				site.bodies = map[string]string{ghIssueAPI: ghRateLimited}
			},
			[]string{
				"github issue: no comments loaded, the stated count not read",
				"the issue's API record was not loaded", "rate limit exhausted",
			},
			ghIssueAPI + "/comments?per_page=100&page=1",
		},
		{
			"stated comments not listed",
			func(site *ghSite) {
				site.api[ghIssueAPI] = strings.Replace(site.api[ghIssueAPI], `"comments": 170`, `"comments": 175`, 1)
			},
			[]string{"170 of 175 comments loaded", "5 stated comment(s) not in the API's list"},
			"",
		},
		{
			"another thread's comments",
			func(site *ghSite) {
				site.api[page2] = strings.ReplaceAll(site.api[page2], "/issues/"+ghIssueNumber+`"`, `/issues/1"`)
			},
			[]string{"100 of 170 comments loaded", "of another thread"},
			"",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			site := ghIssueSite(t)
			tc.edit(site)
			h, _ := site.harvester(t)
			result := h.FetchWithOptions(context.Background(), ghIssueURL, FetchOptions{Refresh: true})
			if result.Error != "" {
				t.Fatalf("fetch failed: %q", result.Error)
			}
			for _, want := range tc.want {
				if !strings.Contains(result.Partial, want) {
					t.Fatalf("the partial marker lacks %q: %q", want, result.Partial)
				}
			}
			if strings.Contains(result.Partial, "203.0.113.7") || strings.Contains(result.Content, "203.0.113.7") {
				t.Fatalf("the artifact repeats the API's message: %q", result.Partial)
			}
			if !strings.Contains(result.Content, "· gaps: ") {
				t.Fatalf("the count line names no gap:\n%.800s", result.Content)
			}
			for _, sent := range site.requests {
				if tc.unsent != "" && sent == tc.unsent {
					t.Fatalf("%s was requested after the rate limit: %v", sent, site.requests)
				}
			}
		})
	}
}

// TestGitHubNonThreadPagesTakeTheGenericPath: a GitHub page that is not an
// issue's or pull request's conversation — a repository, a pull request's
// files, a page naming another item or none — is not claimed.
func TestGitHubNonThreadPagesTakeTheGenericPath(t *testing.T) {
	page := ghFixture(t, "pull-page.html")
	for _, tc := range []struct{ source, page string }{
		{"https://github.com/example-org/example-runtime", page},
		{ghPullURL + "/files", page},
		{ghPullURL, strings.Replace(page, "og:url", "og:urn", 1)},
		{"https://github.com/example-org/example-runtime/pull/1", page},
	} {
		doc, err := html.Parse(strings.NewReader(tc.page))
		if err != nil {
			t.Fatalf("parse the fixture: %v", err)
		}
		if _, name, ok := extractForSite(tc.source, doc); ok {
			t.Fatalf("%s was claimed by %q", tc.source, name)
		}
	}
}
