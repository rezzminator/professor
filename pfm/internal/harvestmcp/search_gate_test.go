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

// TestServerInstructionsNameSearchOnlyWhenConfigured pins every shape of the
// top-level routing text: search_web routing when a backend is configured, a
// search_web-free text plus a one-sentence configuration hint when it is not,
// and read's files only on the local server — never a routing guide
// that recommends a tool the server does not register.
func TestServerInstructionsNameSearchOnlyWhenConfigured(t *testing.T) {
	on := serverInstructions(true, false)
	if !strings.Contains(on, `"search the web for X" is search_web`) ||
		!strings.Contains(on, "for a topic — search_web, then read the URL in urls") {
		t.Fatalf("search-enabled instructions dropped their search routing: %q", on)
	}
	off := serverInstructions(false, false)
	if strings.Contains(off, "search_web") {
		t.Fatalf("search-disabled instructions still name search_web:\n%s", off)
	}
	if !strings.Contains(off, "Web search is not configured on this server") ||
		!strings.Contains(off, "search.searxngURL or search.braveApiKey in harvester.config.json") {
		t.Fatalf("search-disabled instructions lack the configuration hint:\n%s", off)
	}
	if !strings.Contains(on, "in files") || !strings.Contains(on, "then read its handle in publications") {
		t.Fatalf("local instructions do not route local documents:\n%s", on)
	}
	if remote := serverInstructions(true, true); strings.Contains(remote, "in files") {
		t.Fatalf("remote instructions route files, which the remote read never takes:\n%s", remote)
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
