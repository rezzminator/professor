package harvest

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// The fixtures are captured Mastodon answers (testdata/mastodon), trimmed and
// scrubbed: the instance renamed social.example (remote accounts
// remote.example), usernames placeholders, contents replaced by placeholder
// HTML of the captured shapes (h-card mentions, hashtags, a link with
// Mastodon's invisible spans, a line break), the context cut to its first 12
// descendants — a closed subtree, since the API lists them depth first — and
// each replies_count recomputed to the replies kept. status-page.html keeps
// the captured page's metas, its initial-state script (cut to its meta) and
// its #mastodon mount point. reply-*.json is the same thread seen from a
// nested reply: its ancestors and its (empty) subtree.
const (
	mastoID      = "117117221397911074"
	mastoPath    = "/@user-0/" + mastoID
	mastoURL     = "https://social.example" + mastoPath
	mastoAPI     = "/api/v1/statuses/" + mastoID
	mastoReplyID = "117118575452712049"
)

func socialFixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read the captured fixture %s: %v", name, err)
	}
	return string(raw)
}

// socialSite serves pages and API answers by host and request URI, and
// records the API requests (every request to a host in apiHosts).
type socialSite struct {
	mu       sync.Mutex
	answers  map[string]string
	status   map[string]int
	apiHosts map[string]bool
	// apiPath, when set, is a path prefix served as an API answer on any host
	// (a site whose API shares the page's host outside /api/).
	apiPath  string
	requests []string
	headers  []http.Header
}

func (site *socialSite) roundTrip(request *http.Request) (*http.Response, error) {
	key := request.URL.Host + request.URL.RequestURI()
	contentType := "text/html; charset=utf-8"
	activity := strings.HasSuffix(request.URL.Path, "/replies")
	if site.apiHosts[request.URL.Host] || strings.HasPrefix(request.URL.Path, "/api/") || activity ||
		(site.apiPath != "" && strings.HasPrefix(request.URL.Path, site.apiPath)) {
		contentType = "application/json; charset=utf-8"
		if activity {
			contentType = "application/activity+json; charset=utf-8"
		}
		site.mu.Lock()
		site.requests = append(site.requests, key)
		site.headers = append(site.headers, request.Header.Clone())
		site.mu.Unlock()
	}
	if status := site.status[key]; status != 0 {
		if body, ok := site.answers[key]; ok && strings.HasPrefix(contentType, "text/html") {
			// A page served with an error status: a wall in the page's place.
			return response(request, status, contentType, body), nil
		}
		return response(request, status, "application/json", `{"error":"Record not found"}`), nil
	}
	if body, ok := site.answers[key]; ok {
		return response(request, http.StatusOK, contentType, body), nil
	}
	return response(request, http.StatusNotFound, "text/html", "<html><body>not found</body></html>"), nil
}

func (site *socialSite) harvester(t *testing.T) *Harvester {
	t.Helper()
	missing := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusNotFound, "application/json", `{}`), nil
	})
	return mustNew(t, Options{
		CacheDir:    t.TempDir(),
		Client:      &http.Client{Transport: roundTripFunc(site.roundTrip)},
		Chrome:      &http.Client{Transport: roundTripFunc(site.roundTrip)},
		Jina:        &http.Client{Transport: missing},
		OA:          &http.Client{Transport: missing},
		Converter:   &browserSpyConverter{},
		BrowserRung: browserOff(),
		Clock:       newPacingClock(),
	})
}

// servedPage is what the converter makes of a page the extractor did not
// claim: the page as the site served it.
var servedPage = "SERVED PAGE " + strings.Repeat("a paragraph the page itself holds ", 30)

// servingHarvester is site's harvester whose converter stores servedPage.
func (site *socialSite) servingHarvester(t *testing.T) *Harvester {
	t.Helper()
	h := site.harvester(t)
	h.options.Converter = &browserSpyConverter{
		convertFn: func(context.Context, string, string, []byte) (string, error) { return servedPage, nil },
	}
	return h
}

func mastoSite(t *testing.T) *socialSite {
	return &socialSite{answers: map[string]string{
		"social.example" + mastoPath:             socialFixture(t, "mastodon/status-page.html"),
		"social.example" + mastoAPI:              socialFixture(t, "mastodon/status.json"),
		"social.example" + mastoAPI + "/context": socialFixture(t, "mastodon/context.json"),
		"social.example/@user-3/" + mastoReplyID: socialFixture(t, "mastodon/status-page.html"),
		"social.example/api/v1/statuses/" + mastoReplyID: socialFixture(
			t, "mastodon/reply-status.json"),
		"social.example/api/v1/statuses/" + mastoReplyID + "/context": socialFixture(
			t, "mastodon/reply-context.json"),
	}}
}

var mastoEntryRe = regexp.MustCompile(`(?m)^ *- \*\*@[^*]+\*\* · [^·]+ · \[(\d+)\]`)

func mastoRendered(content string) []string {
	var ids []string
	for _, match := range mastoEntryRe.FindAllStringSubmatch(content, -1) {
		ids = append(ids, match[1])
	}
	return ids
}

// TestMastodonStatusLoadsItsRepliesAsATree: a Mastodon page on any host, known
// by its markup, is read from the instance's API — the status's record, then
// its context — sending no credential; every descendant renders once, in the
// API's order, nested under the status it answers, its content as Markdown
// (never raw HTML); the counts reconcile, the artifact is complete, and a
// second harvest is identical.
func TestMastodonStatusLoadsItsRepliesAsATree(t *testing.T) {
	site := mastoSite(t)
	h := site.harvester(t)
	result := h.FetchWithOptions(context.Background(), mastoURL, FetchOptions{Refresh: true})
	if result.Error != "" || result.Partial != "" {
		t.Fatalf("the status is not complete: method=%q partial=%q error=%q\n%.1500s",
			result.Method, result.Partial, result.Error, result.Content)
	}
	var thread struct {
		Descendants []struct {
			ID string `json:"id"`
		} `json:"descendants"`
	}
	if err := json.Unmarshal([]byte(socialFixture(t, "mastodon/context.json")), &thread); err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, reply := range thread.Descendants {
		want = append(want, reply.ID)
	}
	if got := mastoRendered(result.Content); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("replies or their order are wrong: got %v, want %v\n%.2500s", got, want, result.Content)
	}
	for _, text := range []string{
		"# User 0 (@user-0) on social.example",
		"**Post:** " + mastoURL,
		"**Replies:** 12 stated · 12 loaded",
		"The status text, with **emphasis** and a [#example](https://social.example/tags/example) tag.",
		"\n  - **@user-4@remote.example** · ", // a reply to a reply, nested
		"[@user-0](https://social.example/@user-0) Reply 1 with",
	} {
		if !strings.Contains(result.Content, text) {
			t.Fatalf("the artifact lacks %q:\n%.2500s", text, result.Content)
		}
	}
	if strings.Contains(result.Content, "<p>") || strings.Contains(result.Content, "&lt;p") {
		t.Fatalf("the artifact holds raw HTML:\n%.1500s", result.Content)
	}
	wantRequests := "social.example" + mastoAPI + "\nsocial.example" + mastoAPI + "/context"
	if strings.Join(site.requests, "\n") != wantRequests {
		t.Fatalf("API requests %v, want %s", site.requests, wantRequests)
	}
	for _, header := range site.headers {
		if header.Get("Authorization") != "" || header.Get("Cookie") != "" {
			t.Fatalf("an API request carried a credential: %v", header)
		}
	}
	again := h.FetchWithOptions(context.Background(), mastoURL, FetchOptions{Refresh: true})
	if again.Content != result.Content {
		t.Fatal("a second harvest of the same status differs")
	}
}

// TestMastodonUnservedRepliesFlagThePartial: a status stating more replies
// than its context serves renders what was served and names the rest.
func TestMastodonUnservedRepliesFlagThePartial(t *testing.T) {
	site := mastoSite(t)
	site.answers["social.example"+mastoAPI] = strings.Replace(site.answers["social.example"+mastoAPI],
		`"replies_count": 7`, `"replies_count": 131`, 1)
	result := site.harvester(t).FetchWithOptions(context.Background(), mastoURL, FetchOptions{Refresh: true})
	if result.Error != "" || len(mastoRendered(result.Content)) != 12 {
		t.Fatalf("the served replies were not rendered: error=%q\n%.1500s", result.Error, result.Content)
	}
	for _, want := range []string{"12 of 136 stated replies loaded", "124 stated repl(ies) not served"} {
		if !strings.Contains(result.Partial, want) {
			t.Fatalf("the partial marker lacks %q: %q", want, result.Partial)
		}
	}
	if !strings.Contains(result.Content, "**Replies:** 136 stated · 12 loaded") {
		t.Fatalf("the count line does not reconcile:\n%.1500s", result.Content)
	}
}

// TestMastodonReplyRendersItsAncestors: a nested reply's page renders the
// statuses it answers above it, oldest first.
func TestMastodonReplyRendersItsAncestors(t *testing.T) {
	site := mastoSite(t)
	result := site.harvester(t).FetchWithOptions(context.Background(),
		"https://social.example/@user-3/"+mastoReplyID, FetchOptions{Refresh: true})
	if result.Error != "" || result.Partial != "" {
		t.Fatalf("the reply is not complete: partial=%q error=%q", result.Partial, result.Error)
	}
	_, section, found := strings.Cut(result.Content, "## In reply to")
	if !found {
		t.Fatalf("the reply renders no ancestors:\n%.1500s", result.Content)
	}
	root, parent := strings.Index(section, "["+mastoID+"]"), strings.Index(section, "[117117258105671885]")
	if root < 0 || parent < root || strings.Index(section, "## Replies") < parent {
		t.Fatalf("the ancestors are not rendered oldest first above the reply:\n%.1500s", result.Content)
	}
}

// TestMastodonRecordNotLoadedServesThePage: a status whose record the API
// would not answer is not claimed; the page goes the generic path with the
// gap named, and its context is never requested.
func TestMastodonRecordNotLoadedServesThePage(t *testing.T) {
	site := mastoSite(t)
	site.status = map[string]int{"social.example" + mastoAPI: http.StatusNotFound}
	result := site.servingHarvester(t).FetchWithOptions(context.Background(), mastoURL, FetchOptions{Refresh: true})
	if !strings.Contains(result.Partial, "the status's API record was not loaded") {
		t.Fatalf("the partial marker does not name the record: %q (error %q)", result.Partial, result.Error)
	}
	if strings.Join(site.requests, ",") != "social.example"+mastoAPI {
		t.Fatalf("API requests %v, want the record alone", site.requests)
	}
}

// TestMastodonOtherPagesTakeTheGenericPath: a profile page of a Mastodon
// instance and a status address on a page that is not Mastodon's request
// nothing from the API.
func TestMastodonOtherPagesTakeTheGenericPath(t *testing.T) {
	site := mastoSite(t)
	site.answers["social.example/@user-0"] = site.answers["social.example"+mastoPath]
	site.answers["blog.example/@writer/12345"] = "<html><head><title>A post</title></head><body><article><p>" +
		strings.Repeat("An article paragraph. ", 40) + "</p></article></body></html>"
	h := site.harvester(t)
	for _, source := range []string{"https://social.example/@user-0", "https://blog.example/@writer/12345"} {
		h.FetchWithOptions(context.Background(), source, FetchOptions{Refresh: true})
	}
	if len(site.requests) != 0 {
		t.Fatalf("a page that is not a Mastodon status requested %v", site.requests)
	}
}

// TestMastodonStatusIDReadsEveryURIForm: a status's id is read from each form
// an instance names it by — the web address, the classic uri and the uri of
// an account created on a current release (the form a replies collection
// lists such an account's reply by) — and nothing else.
func TestMastodonStatusIDReadsEveryURIForm(t *testing.T) {
	accounts := "users" // the uri segment, spelled apart from the placeholder names
	for path, want := range map[string]string{
		"/@user-0/117117221397911074":                                           "117117221397911074",
		"/" + accounts + "/user-0/statuses/117117221397911074":                  "117117221397911074",
		"/ap/" + accounts + "/117112258603022665/statuses/117117467155130968":   "117117467155130968",
		"/ap/" + accounts + "/117112258603022665/statuses/117117467155130968/x": "",
		"/ap/" + accounts + "/117112258603022665/replies/117117467155130968":    "",
		"/@user-0": "",
	} {
		if got, _ := mastodonStatusID(path); got != want {
			t.Errorf("mastodonStatusID(%q) = %q, want %q", path, got, want)
		}
	}
}
