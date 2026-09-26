package harvest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// listing-votes-p2.json is a captured API answer for page 2 of stackoverflow's
// [rust] listing by votes, 50 to a page (has_more), owners scrubbed to
// placeholders; listing-empty.json the API's captured answer for a tag that
// does not exist.
const (
	seListingURL = "https://stackoverflow.com/questions/tagged/rust?tab=votes&page=2&pagesize=50"
	seListingAPI = "/2.3/questions"
)

var seListedRe = regexp.MustCompile(
	`(?m)^\d+\. \[(?:\\.|[^\]\\])+\]\(https://stackoverflow\.com/questions/(\d+)/[^)]*\)`)

// seListingFixtureIDs reads the question ids of the captured page, in order.
func seListingFixtureIDs(t *testing.T) []string {
	t.Helper()
	var page struct {
		Items []struct {
			ID int64 `json:"question_id"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(seFixture(t, "listing-votes-p2.json")), &page); err != nil {
		t.Fatalf("decode listing-votes-p2.json: %v", err)
	}
	var ids []string
	for _, item := range page.Items {
		ids = append(ids, strconv.FormatInt(item.ID, 10))
	}
	return ids
}

// TestStackExchangeListingReadsThePageFromTheAPI: a tag listing the network
// serves under HTTP 403 — its challenge, or the listing itself — is read from
// the API's /questions with the listing's tags, sort, page and page size, and
// holds every question of the page in the site's order; the count line
// reconciles the page, the later pages are named, the status reported is the
// API's that delivered it, and a second harvest is identical.
func TestStackExchangeListingReadsThePageFromTheAPI(t *testing.T) {
	realPage := "<html><head><title>Highest scored 'rust' questions - Page 2</title></head><body>" +
		"<div id=\"questions\">" + strings.Repeat("<div class=\"s-post-summary\"><h3>A question</h3>"+
		"<p>Score of 204 · 9 answers · 132681 views</p></div>", 43) + "</div></body></html>"
	for name, wall := range map[string]string{"challenge": "", "the page under 403": realPage} {
		t.Run(name, func(t *testing.T) {
			site := &seSite{
				wall:     wall,
				apiError: seFixture(t, "error-bad-parameter.json"),
				api:      map[string]string{seListingAPI + "#2": seFixture(t, "listing-votes-p2.json")},
			}
			if wall == "" {
				site.wall = seFixture(t, "challenge.html")
			}
			h, _ := site.harvester(t)
			var contents []string
			for run := 0; run < 2; run++ {
				result := h.FetchWithOptions(context.Background(), seListingURL, FetchOptions{Refresh: true})
				if result.Error != "" {
					t.Fatalf("the listing failed: status=%d error=%q", result.HTTPStatus, result.Error)
				}
				var got []string
				for _, match := range seListedRe.FindAllStringSubmatch(result.Content, -1) {
					got = append(got, match[1])
				}
				if want := seListingFixtureIDs(t); strings.Join(got, ",") != strings.Join(want, ",") {
					t.Fatalf("questions or their order are wrong: got %d, want %d\n%.2500s",
						len(got), len(want), result.Content)
				}
				if !strings.Contains(result.Content, "50 stated · 50 loaded") ||
					!strings.Contains(result.Partial, "later pages (from page 3) not read") {
					t.Fatalf("the page is not reconciled or its later pages not named: partial=%q\n%.600s",
						result.Partial, result.Content)
				}
				if result.HTTPStatus != http.StatusOK {
					t.Fatalf("http_status %d, want the API's 200 that delivered the page", result.HTTPStatus)
				}
				contents = append(contents, result.Content)
			}
			if contents[0] != contents[1] {
				t.Fatalf("two harvests of the listing differ")
			}
			query := site.queries[0]
			for key, want := range map[string]string{
				"site": "stackoverflow.com", "tagged": "rust", "sort": "votes", "order": "desc",
				"page": "2", "pagesize": "50", "filter": "default", "key": "",
			} {
				if got := query.Get(key); got != want {
					t.Fatalf("API query %s=%q, want %q (%v)", key, got, want, query)
				}
			}
		})
	}
}

// TestStackExchangeListingAddresses: a listing is claimed only for a tab the
// API's /questions answers, the default tab and page size read as the site's.
func TestStackExchangeListingAddresses(t *testing.T) {
	for address, want := range map[string]string{
		"https://stackoverflow.com/questions/tagged/rust":                          "stackoverflow.com rust creation 1 15",
		"https://superuser.com/questions/tagged/tmux+linux?tab=Active&page=3":      "superuser.com tmux;linux activity 3 15",
		"https://stackoverflow.com/questions/tagged/rust?tab=unanswered":           "",
		"https://stackoverflow.com/questions/tagged/rust?pagesize=500":             "",
		"https://stackoverflow.com/questions/123/a-question":                       "",
		"https://api.stackexchange.com/questions/tagged/rust":                      "",
		"https://math.stackexchange.com/questions/tagged/algebra?tab=votes&page=x": "",
	} {
		page, err := url.Parse(address)
		if err != nil {
			t.Fatalf("parse %s: %v", address, err)
		}
		ref, ok := seListingOf(page)
		got := ""
		if ok {
			got = strings.Join([]string{
				ref.site, strings.Join(ref.tags, ";"), ref.sort,
				strconv.Itoa(ref.page), strconv.Itoa(ref.pageSize),
			}, " ")
		}
		if got != want {
			t.Fatalf("%s read as %q, want %q", address, got, want)
		}
	}
}

// TestStackExchangeListingOfNoQuestionsIsNotStored: a listing whose API page
// holds no question (the tag does not exist) is not claimed, so the origin's
// 404 stays an error and nothing is stored.
func TestStackExchangeListingOfNoQuestionsIsNotStored(t *testing.T) {
	origin := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "api.stackexchange.com" {
			return response(request, http.StatusOK, "application/json", seFixture(t, "listing-empty.json")), nil
		}
		return response(request, http.StatusNotFound, "text/html",
			"<html><body><h1>Page not found</h1><p>"+errorPageProse+"</p></body></html>"), nil
	})
	jina := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(
			request,
			http.StatusOK,
			"text/plain",
			"Markdown Content:\n# Page not found\n\n"+errorPageProse,
		), nil
	})
	h := originStatusHarvester(t, origin, jina)
	result := h.FetchWithOptions(context.Background(),
		"https://stackoverflow.com/questions/tagged/no-such-tag?tab=votes", FetchOptions{Refresh: true})
	if result.Error == "" || result.HTTPStatus != http.StatusNotFound {
		t.Fatalf(
			"a missing listing was stored: status=%d method=%q\n%.300s",
			result.HTTPStatus,
			result.Method,
			result.Content,
		)
	}
	if stored := storedArtifacts(t, h.options.CacheDir); len(stored) != 0 {
		t.Fatalf("a missing listing left artifacts: %v", stored)
	}
}
