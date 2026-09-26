package harvest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// The fixtures are captured public AppView answers (testdata/bluesky),
// trimmed and scrubbed: DIDs did:plc:exampleNNNN, handles
// user-N.bsky.example, texts placeholders (the root's with a link facet of
// the captured shape), the thread cut to four direct replies and two replies
// under each reply, three levels deep, and each replyCount recomputed to the
// replies kept. post-page.html keeps the captured page's metas and its
// noscript summary.
const (
	bskyTestPath    = "/profile/user-0.bsky.example/post/3mp54n7zccc2j"
	bskyTestURL     = "https://bsky.app" + bskyTestPath
	bskyTestURI     = "at://did:plc:example0000/app.bsky.feed.post/3mp54n7zccc2j"
	bskyTestResolve = bskyAPIHost + "/xrpc/com.atproto.identity.resolveHandle?handle=user-0.bsky.example"
)

var bskyTestThread = bskyAPIHost + "/xrpc/app.bsky.feed.getPostThread?uri=" + url.QueryEscape(bskyTestURI) +
	"&depth=1000&parentHeight=1000"

func bskySite(t *testing.T) *socialSite {
	return &socialSite{
		apiHosts: map[string]bool{bskyAPIHost: true},
		answers: map[string]string{
			"bsky.app" + bskyTestPath: socialFixture(t, "bluesky/post-page.html"),
			bskyTestResolve:           socialFixture(t, "bluesky/resolve.json"),
			bskyTestThread:            socialFixture(t, "bluesky/thread.json"),
		},
	}
}

var bskyEntryRe = regexp.MustCompile(`(?m)^ *- \*\*@[^*]+\*\* · [^·]+ · \[(at://[^\]]+)\]`)

// bskyThreadURIs lists the fixture thread's reply URIs depth first.
func bskyThreadURIs(t *testing.T) []string {
	t.Helper()
	var answer struct {
		Thread bskyNode `json:"thread"`
	}
	if err := json.Unmarshal([]byte(socialFixture(t, "bluesky/thread.json")), &answer); err != nil {
		t.Fatal(err)
	}
	var uris []string
	var walk func(node *bskyNode)
	walk = func(node *bskyNode) {
		for index := range node.Replies {
			uris = append(uris, node.Replies[index].Post.URI)
			walk(&node.Replies[index])
		}
	}
	walk(&answer.Thread)
	return uris
}

// TestBlueskyPostLoadsItsThreadFromThePublicAppView: a post page is read from
// the public AppView — the handle resolved, then the thread — sending no
// credential; every reply renders once as a tree, the link facet as a link;
// the counts reconcile, the artifact is complete, and a second harvest is
// identical.
func TestBlueskyPostLoadsItsThreadFromThePublicAppView(t *testing.T) {
	site := bskySite(t)
	h := site.harvester(t)
	result := h.FetchWithOptions(context.Background(), bskyTestURL, FetchOptions{Refresh: true})
	if result.Error != "" || result.Partial != "" {
		t.Fatalf("the post is not complete: method=%q partial=%q error=%q\n%.1500s",
			result.Method, result.Partial, result.Error, result.Content)
	}
	want := bskyThreadURIs(t)
	var got []string
	for _, match := range bskyEntryRe.FindAllStringSubmatch(result.Content, -1) {
		got = append(got, match[1])
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("replies or their order are wrong: got %d, want %d\n%.2500s", len(got), len(want), result.Content)
	}
	for _, text := range []string{
		"# User 0 (@user-0.bsky.example) on Bluesky",
		"**Replies:** 9 stated · 9 loaded",
		"Root post text.  \nA second line, see [example.org/page](https://example.org/page)",
		"\n  - **@", // a reply to a reply, nested
	} {
		if !strings.Contains(result.Content, text) {
			t.Fatalf("the artifact lacks %q:\n%.2500s", text, result.Content)
		}
	}
	if strings.Contains(result.Content, "JavaScript Required") {
		t.Fatalf("the artifact holds the page's noscript text:\n%.800s", result.Content)
	}
	if strings.Join(site.requests, "\n") != bskyTestResolve+"\n"+bskyTestThread {
		t.Fatalf("API requests %v, want the handle then the thread", site.requests)
	}
	for _, header := range site.headers {
		if header.Get("Authorization") != "" || header.Get("Cookie") != "" {
			t.Fatalf("an API request carried a credential: %v", header)
		}
	}
	again := h.FetchWithOptions(context.Background(), bskyTestURL, FetchOptions{Refresh: true})
	if again.Content != result.Content {
		t.Fatal("a second harvest of the same post differs")
	}
}

// TestBlueskyUnservedRepliesFlagThePartial: a post stating more replies than
// the AppView serves renders what was served and names the rest.
func TestBlueskyUnservedRepliesFlagThePartial(t *testing.T) {
	site := bskySite(t)
	site.answers[bskyTestThread] = strings.Replace(site.answers[bskyTestThread],
		`"replyCount": 4`, `"replyCount": 1008`, 1)
	result := site.harvester(t).FetchWithOptions(context.Background(), bskyTestURL, FetchOptions{Refresh: true})
	for _, want := range []string{"9 of 1013 stated replies loaded", "1004 stated repl(ies) not served"} {
		if !strings.Contains(result.Partial, want) {
			t.Fatalf("the partial marker lacks %q: %q", want, result.Partial)
		}
	}
	if !strings.Contains(result.Content, "**Replies:** 1013 stated · 9 loaded") {
		t.Fatalf("the count line does not reconcile:\n%.1500s", result.Content)
	}
}

// TestBlueskyThreadNotLoadedServesThePage: a post whose thread the AppView
// would not answer is not claimed; the page goes the generic path with the
// gap named.
func TestBlueskyThreadNotLoadedServesThePage(t *testing.T) {
	site := bskySite(t)
	site.status = map[string]int{bskyTestThread: http.StatusBadRequest}
	h := site.servingHarvester(t)
	result := h.FetchWithOptions(context.Background(), bskyTestURL, FetchOptions{Refresh: true})
	if !strings.Contains(result.Partial, "the post's thread was not loaded from the public AppView") {
		t.Fatalf("the partial marker does not name the thread: %q (error %q)", result.Partial, result.Error)
	}
}
