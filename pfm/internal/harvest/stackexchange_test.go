package harvest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The fixtures are captured Stack Exchange answers, scrubbed: usernames
// placeholders (one kept HTML-escaped, as the API escapes names), bodies
// replaced by placeholder HTML, links renamed; ids, scores, dates, counts,
// accepted marks and structure as the API sent them. question.json is a
// question stating 105 answers and 13 comments; answers-1.json and
// answers-2.json list its answers over two pages (100 + 5, has_more on the
// first), each with its embedded comments (130 in all). challenge.html is the
// Cloudflare challenge the network serves in place of every question page to
// a client that is not a reader's browser (HTTP 403). error-bad-parameter.json
// is a captured API error answer.
const (
	seQuestionID  = "927358"
	seQuestionURL = "https://stackoverflow.com/questions/" + seQuestionID + "/example-question"
	seQuestionAPI = "/2.3/questions/" + seQuestionID
)

// seThrottled is the API's throttle answer: the captured error answer's shape
// with the documented throttle_violation id and name.
const seThrottled = `{"error_id":502,"error_message":"too many requests from this IP, more requests ` +
	`available in 80000 seconds","error_name":"throttle_violation"}`

func seFixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/stackexchange/" + name)
	if err != nil {
		t.Fatalf("read the captured Stack Exchange fixture %s: %v", name, err)
	}
	return string(raw)
}

// seSite serves the network's challenge in place of a question page and the
// API's answers on api.stackexchange.com, by path and page.
type seSite struct {
	mu       sync.Mutex
	api      map[string]string
	status   map[string]int
	bodies   map[string]string
	wall     string
	apiError string
	requests []string
	queries  []url.Values
	headers  []http.Header
}

// seAPIKey is the key of an API answer in seSite: its path, and its page when
// it has one.
func seAPIKey(path string, query url.Values) string {
	if page := query.Get("page"); page != "" {
		return path + "#" + page
	}
	return path
}

func (site *seSite) roundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Host == "api.stackexchange.com" {
		key := seAPIKey(request.URL.Path, request.URL.Query())
		site.mu.Lock()
		site.requests = append(site.requests, key)
		site.queries = append(site.queries, request.URL.Query())
		site.headers = append(site.headers, request.Header.Clone())
		site.mu.Unlock()
		if status := site.status[key]; status != 0 {
			return response(request, status, "application/json; charset=utf-8", site.bodies[key]), nil
		}
		if body, ok := site.api[key]; ok {
			return response(request, http.StatusOK, "application/json; charset=utf-8", body), nil
		}
		return response(request, http.StatusBadRequest, "application/json", site.apiError), nil
	}
	if strings.HasPrefix(request.URL.Path, "/questions/") {
		return response(request, http.StatusForbidden, "text/html; charset=UTF-8", site.wall), nil
	}
	return response(request, http.StatusNotFound, "text/html", "<html><body>not found</body></html>"), nil
}

func (site *seSite) harvester(t *testing.T) (*Harvester, *pacingClock) {
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

func seQuestionSite(t *testing.T) *seSite {
	return &seSite{
		wall:     seFixture(t, "challenge.html"),
		apiError: seFixture(t, "error-bad-parameter.json"),
		api: map[string]string{
			seQuestionAPI:                seFixture(t, "question.json"),
			seQuestionAPI + "/answers#1": seFixture(t, "answers-1.json"),
			seQuestionAPI + "/answers#2": seFixture(t, "answers-2.json"),
		},
	}
}

// seFixtureAnswer is what a test reads of a fixture answer.
type seFixtureAnswer struct {
	ID       int64 `json:"answer_id"`
	Score    int   `json:"score"`
	Created  int64 `json:"creation_date"`
	Accepted bool  `json:"is_accepted"`
	Comments []struct {
		ID int64 `json:"comment_id"`
	} `json:"comments"`
}

// seFixtureAnswers reads the answers of the fixture pages named.
func seFixtureAnswers(t *testing.T, names ...string) []seFixtureAnswer {
	t.Helper()
	var all []seFixtureAnswer
	for _, name := range names {
		var page struct {
			Items []seFixtureAnswer `json:"items"`
		}
		if err := json.Unmarshal([]byte(seFixture(t, name)), &page); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		all = append(all, page.Items...)
	}
	return all
}

var (
	seAnswerRe = regexp.MustCompile(
		`(?m)^### Answer \[#(\d+)\]\(https://stackoverflow\.com/a/(\d+)\) · \*\*[^*]+\*\* · \d{4}-\d\d-\d\d \d\d:\d\d UTC · score (-?\d+)( · \*\*accepted\*\*)?$`,
	)
	seCommentRe = regexp.MustCompile(
		`(?m)^- \*\*[^*]+\*\* · \d{4}-\d\d-\d\d \d\d:\d\d UTC · score -?\d+ · \[comment #(\d+)\]\(https://stackoverflow\.com/posts/comments/(\d+)\)$`,
	)
)

// TestStackExchangeQuestionLoadsEveryAnswerAndComment: a question whose page
// the network walls is read from the API — its record, then both answer pages
// at the loader pace, sending no credential or key — and renders every answer
// once, by score, the accepted one marked, and every comment on every post;
// both counts reconcile, the artifact is complete, and a second harvest is
// identical.
func TestStackExchangeQuestionLoadsEveryAnswerAndComment(t *testing.T) {
	site := seQuestionSite(t)
	h, pacing := site.harvester(t)
	result := h.FetchWithOptions(context.Background(), seQuestionURL, FetchOptions{Refresh: true})
	if result.Error != "" || result.Partial != "" {
		t.Fatalf("the question is not complete: method=%q partial=%q error=%q\n%.1500s",
			result.Method, result.Partial, result.Error, result.Content)
	}
	answers := seFixtureAnswers(t, "answers-1.json", "answers-2.json")
	sort.SliceStable(answers, func(a, b int) bool {
		if answers[a].Score != answers[b].Score {
			return answers[a].Score > answers[b].Score
		}
		return answers[a].Created < answers[b].Created
	})
	var want, got []string
	accepted := ""
	for _, answer := range answers {
		want = append(want, strconv.FormatInt(answer.ID, 10))
	}
	for _, match := range seAnswerRe.FindAllStringSubmatch(result.Content, -1) {
		if match[1] != match[2] {
			t.Fatalf("answer #%s links to /a/%s", match[1], match[2])
		}
		got = append(got, match[1])
		if match[4] != "" {
			accepted += match[1]
		}
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("answers or their order are wrong: got %d, want %d\n%.1500s", len(got), len(want), result.Content)
	}
	if accepted != "927386" {
		t.Fatalf("the accepted answer marked is %q, want 927386", accepted)
	}
	wantComments := map[string]bool{}
	var question struct {
		Items []struct {
			Comments []struct {
				ID int64 `json:"comment_id"`
			} `json:"comments"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(seFixture(t, "question.json")), &question); err != nil {
		t.Fatalf("decode question.json: %v", err)
	}
	for _, comment := range question.Items[0].Comments {
		wantComments[strconv.FormatInt(comment.ID, 10)] = true
	}
	for _, answer := range answers {
		for _, comment := range answer.Comments {
			wantComments[strconv.FormatInt(comment.ID, 10)] = true
		}
	}
	gotComments := map[string]bool{}
	for _, match := range seCommentRe.FindAllStringSubmatch(result.Content, -1) {
		if gotComments[match[1]] || match[1] != match[2] {
			t.Fatalf("comment #%s rendered twice or misliked", match[1])
		}
		gotComments[match[1]] = true
	}
	if len(gotComments) != 143 || len(wantComments) != 143 {
		t.Fatalf("rendered %d comments, want the fixtures' %d (143)", len(gotComments), len(wantComments))
	}
	for id := range wantComments {
		if !gotComments[id] {
			t.Fatalf("comment #%s is not rendered", id)
		}
	}
	for _, text := range []string{
		"# Example question about \"undo\"\n",
		"**Asked by:** user-1 · **Asked:** 2009-05-29 18:09 UTC · **Score:** ",
		"**Tags:** git, example-tag",
		"**Question:** https://stackoverflow.com/questions/927358/example-question  \n",
		"**Answers:** 105 stated · 105 loaded · **Comments:** 143 stated · 143 loaded, on the question and the answers loaded\n",
		"- point one\n- point two",
		"```\ngit reset HEAD~1\n```",
		"Try `git log --oneline` first; see [the doc](https://example.com/doc).",
		"**user-7 & co**",
	} {
		if !strings.Contains(result.Content, text) {
			t.Fatalf("the artifact lacks %q:\n%.2500s", text, result.Content)
		}
	}
	wantRequests := []string{seQuestionAPI, seQuestionAPI + "/answers#1", seQuestionAPI + "/answers#2"}
	if strings.Join(site.requests, "\n") != strings.Join(wantRequests, "\n") || len(pacing.sleeps) != 3 {
		t.Fatalf(
			"API requests %v paced %d times, want %v paced 3 times",
			site.requests,
			len(pacing.sleeps),
			wantRequests,
		)
	}
	for index, query := range site.queries {
		if query.Get("site") != "stackoverflow.com" || query.Get("filter") != seFilter || query.Get("key") != "" {
			t.Fatalf("API request %d asked site=%q filter=%q key=%q", index, query.Get("site"), query.Get("filter"),
				query.Get("key"))
		}
		if index > 0 &&
			(query.Get("pagesize") != "100" || query.Get("sort") != "creation" || query.Get("order") != "asc") {
			t.Fatalf("answers request %d asked %v, not 100 a page oldest first", index, query)
		}
	}
	for _, header := range site.headers {
		if header.Get("Authorization") != "" || header.Get("Cookie") != "" {
			t.Fatalf("an API request carried a credential: %v", header)
		}
	}
	again := h.FetchWithOptions(context.Background(), seQuestionURL, FetchOptions{Refresh: true})
	if again.Content != result.Content {
		t.Fatal("a second harvest of the same question differs")
	}
}

// TestStackExchangeGapsFlagThePartial: whatever the question advertises that
// was not loaded — an answer page refused or throttled, stated answers or
// comments the API did not list, another question's answers, an answer
// stating no comment count — flags the artifact partial and is named.
func TestStackExchangeGapsFlagThePartial(t *testing.T) {
	page2 := seQuestionAPI + "/answers#2"
	for _, tc := range []struct {
		name   string
		edit   func(site *seSite)
		want   []string
		unsent string
	}{
		{
			"an answer page refused",
			func(site *seSite) { site.status = map[string]int{page2: http.StatusInternalServerError} },
			[]string{
				"stackexchange question: 100 of 105 answers", "answers page 2 not loaded (answers from #101 on",
				"HTTP 500",
			},
			"",
		},
		{
			"the throttle",
			func(site *seSite) {
				site.status = map[string]int{page2: http.StatusBadRequest}
				site.bodies = map[string]string{page2: seThrottled}
			},
			[]string{
				"100 of 105 answers", "answers page 2 not loaded",
				"the site answered HTTP 400 (rate limit exhausted) after 3 request(s); not retried",
			},
			"",
		},
		{
			"stated answers not listed",
			func(site *seSite) {
				site.api[seQuestionAPI] = strings.Replace(site.api[seQuestionAPI], `"answer_count": 105`,
					`"answer_count": 107`, 1)
			},
			[]string{"105 of 107 answers", "2 stated answer(s) not in the API's list"},
			"",
		},
		{
			"stated comments not listed",
			func(site *seSite) {
				site.api[seQuestionAPI] = strings.Replace(site.api[seQuestionAPI], `"comment_count": 13`,
					`"comment_count": 15`, 1)
			},
			[]string{"143 of 145 stated comments loaded", "the question: 15 stated comment(s) · 13 loaded"},
			"",
		},
		{
			"another question's answers",
			func(site *seSite) {
				site.api[page2] = strings.ReplaceAll(site.api[page2], `"question_id": 927358`, `"question_id": 1`)
			},
			[]string{"100 of 105 answers", "of another question (#1)"},
			"",
		},
		{
			"an answer stating no comment count",
			func(site *seSite) {
				site.api[page2] = strings.Replace(site.api[page2], `"comment_count": 3,`, "", 1)
			},
			[]string{"140 of 140 stated comments loaded", ": the stated comment count was not read"},
			"",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			site := seQuestionSite(t)
			tc.edit(site)
			h, _ := site.harvester(t)
			result := h.FetchWithOptions(context.Background(), seQuestionURL, FetchOptions{Refresh: true})
			if result.Error != "" {
				t.Fatalf("fetch failed: %q", result.Error)
			}
			for _, want := range tc.want {
				if !strings.Contains(result.Partial, want) {
					t.Fatalf("the partial marker lacks %q: %q", want, result.Partial)
				}
			}
			if strings.Contains(result.Partial, "80000") || strings.Contains(result.Content, "80000") {
				t.Fatalf("the artifact repeats the API's message: %q", result.Partial)
			}
			if !strings.Contains(result.Content, "· gaps: ") {
				t.Fatalf("the count line names no gap:\n%.800s", result.Content)
			}
			for _, sent := range site.requests {
				if tc.unsent != "" && sent == tc.unsent {
					t.Fatalf("%s was requested after the throttle: %v", sent, site.requests)
				}
			}
		})
	}
}

// TestStackExchangeHonoursTheAPIBackoff: an answer asking for a back-off
// delays the next request by it; one asking for longer than a fetch waits
// ends the following, named, the next page never requested.
func TestStackExchangeHonoursTheAPIBackoff(t *testing.T) {
	withBackoff := func(site *seSite, seconds int) {
		site.api[seQuestionAPI+"/answers#1"] = strings.Replace(site.api[seQuestionAPI+"/answers#1"],
			`"has_more": true,`, `"has_more": true, "backoff": `+strconv.Itoa(seconds)+`,`, 1)
	}
	site := seQuestionSite(t)
	withBackoff(site, 10)
	h, pacing := site.harvester(t)
	result := h.FetchWithOptions(context.Background(), seQuestionURL, FetchOptions{Refresh: true})
	if result.Error != "" || result.Partial != "" {
		t.Fatalf("a honoured back-off left the question incomplete: partial=%q error=%q", result.Partial, result.Error)
	}
	want := []time.Duration{loaderPace, loaderPace, 10 * time.Second}
	if len(pacing.sleeps) != 3 || pacing.sleeps[2] != want[2] || pacing.sleeps[0] != want[0] {
		t.Fatalf("requests waited %v, want %v", pacing.sleeps, want)
	}

	site = seQuestionSite(t)
	withBackoff(site, 120)
	h, _ = site.harvester(t)
	result = h.FetchWithOptions(context.Background(), seQuestionURL, FetchOptions{Refresh: true})
	for _, text := range []string{"100 of 105 answers", "the site asked for a 2m0s back-off"} {
		if !strings.Contains(result.Partial, text) {
			t.Fatalf("the partial marker lacks %q: %q", text, result.Partial)
		}
	}
	for _, sent := range site.requests {
		if sent == seQuestionAPI+"/answers#2" {
			t.Fatalf("page 2 was requested inside a 120 s back-off: %v", site.requests)
		}
	}
}

// TestStackExchangeClaimsQuestionsOnEveryNetworkSite: a question on any site
// of the network is read with that site as the API's site; any other address
// on the network's hosts, and every other host, is not a question.
func TestStackExchangeClaimsQuestionsOnEveryNetworkSite(t *testing.T) {
	for raw, want := range map[string]string{
		"https://superuser.com/questions/209437/how-do-i-scroll-in-tmux": "superuser.com#209437",
		"https://math.stackexchange.com/questions/1/x":                   "math.stackexchange.com#1",
		"https://ru.stackoverflow.com/q/77/12":                           "ru.stackoverflow.com#77",
		"https://www.mathoverflow.net/questions/5/slug/6":                "mathoverflow.net#5",
		"https://meta.stackexchange.com/questions/9/":                    "meta.stackexchange.com#9",
		"https://askubuntu.com/questions/3":                              "askubuntu.com#3",
		"https://stackoverflow.com/questions/tagged/git":                 "",
		"https://stackoverflow.com/questions/ask":                        "",
		"https://stackoverflow.com/u/1":                                  "",
		"https://stackoverflow.com/questions/1/slug/2/extra":             "",
		"https://stackexchange.com/questions/1":                          "",
		"https://api.stackexchange.com/questions/1":                      "",
		"https://chat.stackexchange.com/questions/1":                     "",
		"https://notstackoverflow.com/questions/1":                       "",
		"https://example.com/questions/1":                                "",
	} {
		parsed, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		ref, ok := seQuestionOf(parsed)
		got := ""
		if ok {
			got = ref.site + "#" + strconv.FormatInt(ref.id, 10)
		}
		if got != want {
			t.Fatalf("%s read as %q, want %q", raw, got, want)
		}
	}
	site := seQuestionSite(t)
	h, _ := site.harvester(t)
	result := h.FetchWithOptions(context.Background(), "https://superuser.com/questions/"+seQuestionID+"/x",
		FetchOptions{Refresh: true})
	if result.Error != "" || len(site.queries) == 0 || site.queries[0].Get("site") != "superuser.com" {
		t.Fatalf("a superuser.com question was not read from its site: error=%q queries=%v", result.Error, site.queries)
	}
	if !strings.Contains(result.Content, "[#927386](https://superuser.com/a/927386)") {
		t.Fatalf("answers do not link to their own site:\n%.800s", result.Content)
	}
}

// TestStackExchangeRecordNotLoadedFallsToTheBrowser: when the throttle
// refuses the question's own API record, the extractor does not claim the
// walled page with an empty stub: the ladder goes on to the browser rung,
// which stores the page it renders with the record's gap and the throttle
// named in its partial marker, the record never asked again and no answer page
// requested. With the browser rung off, the wall is a failed fetch, never
// stored.
func TestStackExchangeRecordNotLoadedFallsToTheBrowser(t *testing.T) {
	rendered := "RENDERED QUESTION " + strings.Repeat("an answer the browser rendered ", 30)
	for _, browser := range []bool{true, false} {
		site := seQuestionSite(t)
		site.status = map[string]int{seQuestionAPI: http.StatusBadRequest}
		site.bodies = map[string]string{seQuestionAPI: seThrottled}
		h, _ := site.harvester(t)
		spy := &browserSpyConverter{
			html:   "<html><head><title>Example question</title></head><body><p>question page</p></body></html>",
			status: http.StatusOK,
			convertFn: func(_ context.Context, _, _ string, body []byte) (string, error) {
				if isChallenge(body, http.StatusForbidden) {
					return "Just a moment...", nil
				}
				return rendered, nil
			},
		}
		h.options.Converter = spy
		h.settings.browser = browser
		result := h.FetchWithOptions(context.Background(), seQuestionURL, FetchOptions{Refresh: true})
		if strings.Join(site.requests, ",") != seQuestionAPI {
			t.Fatalf("browser=%v: API requests %v, want the record alone", browser, site.requests)
		}
		if !browser {
			if result.Error == "" || strings.Contains(result.Content, "nothing of the question") {
				t.Fatalf("the wall was stored with the browser rung off (method %q):\n%.600s", result.Method,
					result.Content)
			}
			continue
		}
		if result.Error != "" || result.Method != "browser-chrome" || spy.browserCalls == 0 {
			t.Fatalf("the browser render was not stored: method=%q browser=%d error=%q", result.Method,
				spy.browserCalls, result.Error)
		}
		if !strings.Contains(result.Content, "RENDERED QUESTION") {
			t.Fatalf("the artifact is not the browser's render:\n%.600s", result.Content)
		}
		for _, want := range []string{"the question's API record was not loaded", "rate limit exhausted"} {
			if !strings.Contains(result.Partial, want) {
				t.Fatalf("the partial marker lacks %q: %q", want, result.Partial)
			}
		}
		if strings.Contains(result.Partial, "80000") {
			t.Fatalf("the artifact repeats the API's message: %q", result.Partial)
		}
	}
}

// TestStackExchangeSpentQuotaEndsTheFollowing: an answer reporting the
// address's daily quota spent (quota_remaining 0) ends the following, named,
// before another request is sent — the answer it came with still kept.
func TestStackExchangeSpentQuotaEndsTheFollowing(t *testing.T) {
	site := seQuestionSite(t)
	site.api[seQuestionAPI+"/answers#1"] = strings.Replace(site.api[seQuestionAPI+"/answers#1"],
		`"quota_remaining": 293`, `"quota_remaining": 0`, 1)
	h, _ := site.harvester(t)
	result := h.FetchWithOptions(context.Background(), seQuestionURL, FetchOptions{Refresh: true})
	if result.Error != "" {
		t.Fatalf("fetch failed: %q", result.Error)
	}
	for _, want := range []string{"100 of 105 answers", "answers page 2 not loaded", "quota"} {
		if !strings.Contains(result.Partial, want) {
			t.Fatalf("the partial marker lacks %q: %q", want, result.Partial)
		}
	}
	for _, sent := range site.requests {
		if sent == seQuestionAPI+"/answers#2" {
			t.Fatalf("page 2 was requested after the quota was spent: %v", site.requests)
		}
	}
}
