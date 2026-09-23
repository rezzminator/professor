package harvest

import (
	"context"
	"strings"
	"testing"
)

// TestMastodonRepliesCollectionReachesPastTheContext: a status stating more
// replies than its context served reads its ActivityPub replies collection on
// its own host, page by page; the host's own reply found there loads as an API
// record with its own context (its subtree), a remote reply the host already
// served counts as loaded, and one it never served is named "remote, not
// fetched" — another instance is never asked.
func TestMastodonRepliesCollectionReachesPastTheContext(t *testing.T) {
	site := mastoSite(t)
	collection := "social.example/@user-0/" + mastoID + "/replies"
	local := "social.example/api/v1/statuses/117117378316307585"
	site.answers["social.example"+mastoAPI] = strings.Replace(site.answers["social.example"+mastoAPI],
		`"replies_count": 7`, `"replies_count": 9`, 1)
	site.answers[collection] = socialFixture(t, "mastodon/replies.json")
	site.answers[collection+"?only_other_accounts=true&page=true"] = socialFixture(t, "mastodon/replies-page.json")
	site.answers[collection+"?min_id=117117378316307585&only_other_accounts=true&page=true"] = socialFixture(
		t, "mastodon/replies-page-2.json")
	site.answers[local] = socialFixture(t, "mastodon/local-reply-status.json")
	site.answers[local+"/context"] = socialFixture(t, "mastodon/local-reply-context.json")
	h := site.harvester(t)
	result := h.FetchWithOptions(context.Background(), mastoURL, FetchOptions{Refresh: true})
	if result.Error != "" {
		t.Fatalf("the status failed: %q", result.Error)
	}
	if got := mastoRendered(result.Content); len(got) != 14 {
		t.Fatalf("rendered %d replies %v, want the 12 of the context and 2 reached past it\n%.3000s",
			len(got), got, result.Content)
	}
	for _, text := range []string{
		"**Replies:** 15 stated · 14 loaded (9 stated to the post itself, the rest to its replies) · 1 remote, not fetched",
		"\n- **@user-11** · ",
		"\n  - **@user-0** · ", // the local reply's own reply, from its context
		"- *a reply remote, not fetched (hosted on another instance and never served by this one)* · " +
			"https://remote.example/@user-12/117117390641172839",
	} {
		if !strings.Contains(result.Content, text) {
			t.Fatalf("the artifact lacks %q:\n%.3000s", text, result.Content)
		}
	}
	if !strings.Contains(result.Partial, "14 of 15 stated replies loaded — 1 repl(ies) remote, not fetched") ||
		strings.Contains(result.Partial, "not served") {
		t.Fatalf("the partial marker does not name the remote reply alone: %q", result.Partial)
	}
	wantRequests := []string{
		"social.example" + mastoAPI, "social.example" + mastoAPI + "/context", collection,
		collection + "?only_other_accounts=true&page=true",
		collection + "?min_id=117117378316307585&only_other_accounts=true&page=true",
		local, local + "/context",
	}
	if strings.Join(site.requests, "\n") != strings.Join(wantRequests, "\n") {
		t.Fatalf("API requests\n%s\nwant\n%s", strings.Join(site.requests, "\n"), strings.Join(wantRequests, "\n"))
	}
	for index, request := range site.requests {
		if strings.Contains(request, "/replies") &&
			site.headers[index].Get("Accept") != "application/activity+json" {
			t.Fatalf("the collection %s was asked without the ActivityPub Accept: %v", request, site.headers[index])
		}
	}
	again := h.FetchWithOptions(context.Background(), mastoURL, FetchOptions{Refresh: true})
	if again.Content != result.Content {
		t.Fatal("a second harvest of the same status differs")
	}
}
