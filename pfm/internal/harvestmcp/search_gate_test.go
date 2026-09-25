package harvestmcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/harvest"
)

// TestServerInstructionsNameSearchOnlyWhenConfigured pins serverInstructions
// to the contracts' harvester part, byte for byte, for each of the four
// combinations: clause 3 (local documents) only when local, clause 6 (web
// search) only when a backend is configured — never a routing guide that
// recommends a tool the server does not register. Every combination ends with
// the per-item empty-versus-error rule; search off adds the enable hint.
func TestServerInstructionsNameSearchOnlyWhenConfigured(t *testing.T) {
	clause1 := `Read a web page → harvester_read with it in urls`
	clause2 := `a paper or book by DOI, arXiv id, PMID, PMCID, ISBN, landing URL or harvester_search_literature handle → harvester_read with it in publications`
	clause3 := `a local document → harvester_read with its path in files`
	clause4 := `find papers or books by title → harvester_search_literature`
	clause5 := `save a file's bytes unparsed → harvester_download_file`
	clause6 := `search the web for a topic → harvester_search_web`

	const perItem = ` Every tool answers per item — an empty list is "nothing found", an error is "the lookup failed", never one shape for both.`
	const searchOff = ` Web search is not configured on this server — set search.searxngURL or search.braveApiKey in harvester.config.json to enable web search.`
	join := func(clauses ...string) string { return strings.Join(clauses, "; ") + "." + perItem }
	joinOff := func(clauses ...string) string { return join(clauses...) + searchOff }

	for _, test := range []struct {
		name            string
		searchAvailable bool
		remote          bool
		want            string
	}{
		{"local, search off", false, false, joinOff(clause1, clause2, clause3, clause4, clause5)},
		{"local, search on", true, false, join(clause1, clause2, clause3, clause4, clause5, clause6)},
		{"remote, search off", false, true, joinOff(clause1, clause2, clause4, clause5)},
		{"remote, search on", true, true, join(clause1, clause2, clause4, clause5, clause6)},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := serverInstructions(test.searchAvailable, test.remote)
			if !test.searchAvailable && strings.Contains(got, "harvester_search_web") {
				t.Fatalf("search off, instructions name harvester_search_web: %q", got)
			}
			if got != test.want {
				t.Fatalf(
					"serverInstructions(%v, %v) =\n%q\nwant\n%q",
					test.searchAvailable,
					test.remote,
					got,
					test.want,
				)
			}
		})
	}
}

// A failed search renders every backend's own error, one per line.
func TestSearchFailureRendersEachBackend(t *testing.T) {
	text := renderSearchFailure(
		errors.Join(errors.New("searxng http://127.0.0.1:8888: HTTP 502"), errors.New("brave: HTTP 401")),
	)
	if !strings.Contains(text, "Web search failed") || !strings.Contains(text, "could not classify") {
		t.Fatalf("search failure lost safe public message: %q", text)
	}
	for _, name := range []string{"harvester_read", "harvester_search_literature", "harvester_download_file"} {
		if !strings.Contains(text, name) {
			t.Fatalf("search failure text %q does not name %q", text, name)
		}
	}
	for _, secret := range []string{"searxng", "127.0.0.1", "HTTP 502", "brave: HTTP 401", "harvester.config.json"} {
		if strings.Contains(text, secret) {
			t.Fatalf("search failure text exposed private backend detail %q:\n%s", secret, text)
		}
	}
	if strings.Contains(text, "unreachable or failing") {
		t.Fatalf("search failure text kept the generic message:\n%s", text)
	}
}

// TestRenderSearchFailureRendersConfigurationStatesVerbatim pins the two
// configuration failures a caller must tell apart from a backend outage:
// neither can ever succeed on retry, so they render exactly as harvest names
// them rather than through the generic backend/outage wording.
func TestRenderSearchFailureRendersConfigurationStatesVerbatim(t *testing.T) {
	if got := renderSearchFailure(harvest.ErrSearchNotConfigured); got != harvest.ErrSearchNotConfigured.Error() {
		t.Fatalf(
			"renderSearchFailure(not configured) = %q, want the sentinel verbatim %q",
			got,
			harvest.ErrSearchNotConfigured.Error(),
		)
	}
	if got := renderSearchFailure(harvest.ErrSearchDisabled); got != harvest.ErrSearchDisabled.Error() {
		t.Fatalf(
			"renderSearchFailure(disabled) = %q, want the sentinel verbatim %q",
			got,
			harvest.ErrSearchDisabled.Error(),
		)
	}
}

// TestRenderSearchFailureNamesTheBackendConnectionCause is the regression for
// a refused SearXNG backend rendering as the generic "Retrieval failed. Retry
// or choose another work." — indistinguishable from every other failure kind
// and naming neither the backend nor a real cause.
func TestRenderSearchFailureNamesTheBackendConnectionCause(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closedURL := server.URL
	server.Close() // closed before use: every dial to it is refused.

	_, _, err := harvest.Search(context.Background(), "q", harvest.SearchOptions{SearXNGURL: closedURL})
	if err == nil {
		t.Fatal("Search against a closed SearXNG returned no error")
	}
	text := renderSearchFailure(err)
	if !strings.Contains(text, "searxng") {
		t.Fatalf("search failure %q does not name the searxng backend", text)
	}
	if !strings.Contains(text, "connection refused") {
		t.Fatalf("search failure %q does not name a connection cause", text)
	}
	if strings.Contains(text, "Retry or choose another work") {
		t.Fatalf("search failure %q fell back to the generic outage message", text)
	}
}

// TestRenderSearchFailureHintsSearXNGJSONFormatOn403 pins the operator's most
// common misconfiguration: SearXNG answers every request with 403 until its
// settings.yml turns on the json format, and the default HTTP-403 wording
// ("forbidden") never says so.
func TestRenderSearchFailureHintsSearXNGJSONFormatOn403(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer server.Close()

	_, _, err := harvest.Search(context.Background(), "q", harvest.SearchOptions{SearXNGURL: server.URL})
	if err == nil {
		t.Fatal("Search against a 403-answering SearXNG returned no error")
	}
	text := renderSearchFailure(err)
	if !strings.Contains(text, "searxng") || !strings.Contains(text, "403") {
		t.Fatalf("search failure %q does not name searxng/403", text)
	}
	if !strings.Contains(text, "settings.yml") || !strings.Contains(text, "json format") {
		t.Fatalf("search failure %q lacks the SearXNG json-format hint", text)
	}
}
