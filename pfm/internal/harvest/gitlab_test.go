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

// The fixtures are captured GitLab GraphQL answers (testdata/gitlab) for one
// issue's notes, trimmed and scrubbed: the project renamed group-0/project-0,
// usernames placeholders, bodies placeholder Markdown, the first answer cut to
// its first six notes (one of them a system note) and the second to the next
// four, each userNotesCount recomputed to the user notes kept. The first
// answer keeps its captured cursor, which the second page is asked after.
// issue-page.html keeps the captured page's metas (og:site_name GitLab).
const gitlabIssuePath = "/group-0/project-0/-/issues/1822"

// gitlabQueryFor is the query the extractor sends for one page of the issue's
// notes, spelled out here so the test pins the wire.
func gitlabQueryFor(after string) string {
	cursor := ""
	if after != "" {
		cursor = `, after: "` + after + `"`
	}
	return `{ project(fullPath: "group-0/project-0") { issue(iid: "1822") { iid title webUrl createdAt state ` +
		`author { username } description userNotesCount notes(first: 100` + cursor + `) { pageInfo { hasNextPage ` +
		`endCursor } nodes { id system body createdAt url author { username } discussion { id } } } } } }`
}

func gitlabAPIKey(host, after string) string {
	return host + "/api/graphql?" + url.Values{"query": {gitlabQueryFor(after)}}.Encode()
}

type gitlabFixture struct {
	Data struct {
		Project struct {
			Issue struct {
				Notes struct {
					PageInfo struct {
						EndCursor string `json:"endCursor"`
					} `json:"pageInfo"`
					Nodes []struct {
						ID     string `json:"id"`
						System bool   `json:"system"`
					} `json:"nodes"`
				} `json:"notes"`
			} `json:"issue"`
		} `json:"project"`
	} `json:"data"`
}

func gitlabSite(t *testing.T, host string) (site *socialSite, userNotes []string) {
	t.Helper()
	var first, second gitlabFixture
	for name, into := range map[string]*gitlabFixture{"gitlab/notes-1.json": &first, "gitlab/notes-2.json": &second} {
		if err := json.Unmarshal([]byte(socialFixture(t, name)), into); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
	}
	for _, page := range []gitlabFixture{first, second} {
		for _, node := range page.Data.Project.Issue.Notes.Nodes {
			if !node.System {
				userNotes = append(userNotes, node.ID[strings.LastIndex(node.ID, "/")+1:])
			}
		}
	}
	cursor := first.Data.Project.Issue.Notes.PageInfo.EndCursor
	return &socialSite{answers: map[string]string{
		host + gitlabIssuePath:     socialFixture(t, "gitlab/issue-page.html"),
		gitlabAPIKey(host, ""):     socialFixture(t, "gitlab/notes-1.json"),
		gitlabAPIKey(host, cursor): socialFixture(t, "gitlab/notes-2.json"),
	}}, userNotes
}

var gitlabNoteRe = regexp.MustCompile(`(?m)^ *- \*\*[^*]+\*\* · [^·]+ · \[#(\d+)\]`)

// TestGitLabIssueLoadsEveryNote: a GitLab issue's notes are read from the
// instance's GraphQL API, page after page by cursor, sending no credential;
// every user note renders once in time order — a discussion's replies nested
// under its first note — the system notes left out and counted; the counts
// reconcile, the artifact is complete, and a second harvest is identical. On
// gitlab.com (by host) and on a self-hosted instance (by its markup) alike.
func TestGitLabIssueLoadsEveryNote(t *testing.T) {
	for _, host := range []string{"gitlab.com", "gitlab.example"} {
		site, want := gitlabSite(t, host)
		h := site.harvester(t)
		result := h.FetchWithOptions(context.Background(), "https://"+host+gitlabIssuePath, FetchOptions{Refresh: true})
		if result.Error != "" || result.Partial != "" {
			t.Fatalf("%s: the issue is not complete: partial=%q error=%q\n%.1500s",
				host, result.Partial, result.Error, result.Content)
		}
		var got []string
		for _, match := range gitlabNoteRe.FindAllStringSubmatch(result.Content, -1) {
			got = append(got, match[1])
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("%s: notes or their order are wrong: got %v, want %v\n%.2500s", host, got, want, result.Content)
		}
		for _, text := range []string{
			"# An issue title (#1822)",
			"**Notes:** 9 stated · 9 loaded (1 system note(s) not shown)",
			"The issue body, with **bold** and a list:",
			"\n  - **user-", // a reply in a discussion, nested
			"Note 1 with `code` and **bold**.",
		} {
			if !strings.Contains(result.Content, text) {
				t.Fatalf("%s: the artifact lacks %q:\n%.2500s", host, text, result.Content)
			}
		}
		if len(site.requests) != 2 {
			t.Fatalf("%s: API requests %v, want the two note pages", host, site.requests)
		}
		for _, header := range site.headers {
			if header.Get("Authorization") != "" || header.Get("Cookie") != "" {
				t.Fatalf("an API request carried a credential: %v", header)
			}
		}
		again := h.FetchWithOptions(context.Background(), "https://"+host+gitlabIssuePath, FetchOptions{Refresh: true})
		if again.Content != result.Content {
			t.Fatalf("%s: a second harvest of the same issue differs", host)
		}
	}
}

// TestGitLabUnreadPageFlagsThePartial: a notes page the API would not answer
// is named, and the notes it held are counted as not loaded.
func TestGitLabUnreadPageFlagsThePartial(t *testing.T) {
	site, _ := gitlabSite(t, "gitlab.com")
	var first gitlabFixture
	if err := json.Unmarshal([]byte(site.answers[gitlabAPIKey("gitlab.com", "")]), &first); err != nil {
		t.Fatal(err)
	}
	site.status = map[string]int{
		gitlabAPIKey("gitlab.com", first.Data.Project.Issue.Notes.PageInfo.EndCursor): http.StatusInternalServerError,
	}
	result := site.harvester(t).FetchWithOptions(context.Background(), "https://gitlab.com"+gitlabIssuePath,
		FetchOptions{Refresh: true})
	for _, want := range []string{"gitlab issue: 5 of 9 notes loaded", "notes page 2 not loaded"} {
		if !strings.Contains(result.Partial, want) {
			t.Fatalf("the partial marker lacks %q: %q (error %q)", want, result.Partial, result.Error)
		}
	}
	if !strings.Contains(result.Content, "**Notes:** 9 stated · 5 loaded") {
		t.Fatalf("the count line does not reconcile:\n%.1500s", result.Content)
	}
}

// TestGitLabRecordNotLoadedServesThePage: an issue whose first answer never
// loaded is not claimed; the page goes the generic path, the gap named.
func TestGitLabRecordNotLoadedServesThePage(t *testing.T) {
	site, _ := gitlabSite(t, "gitlab.com")
	site.status = map[string]int{gitlabAPIKey("gitlab.com", ""): http.StatusNotFound}
	result := site.servingHarvester(t).FetchWithOptions(context.Background(), "https://gitlab.com"+gitlabIssuePath,
		FetchOptions{Refresh: true})
	if !strings.Contains(result.Partial, "the issue's API record was not loaded") {
		t.Fatalf("the partial marker does not name the record: %q (error %q)", result.Partial, result.Error)
	}
}
