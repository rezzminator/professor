package harvest

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type searchRoundTrip func(*http.Request) (*http.Response, error)

func (fn searchRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

// A self-hosted SearXNG on loopback is the deployment the search tool
// recommends first. Its URL is operator configuration, so the search request
// must reach it (GitHub #21: the SSRF guard refused 127.0.0.1 outright).
func TestSearchReachesOperatorConfiguredLoopbackSearXNG(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" || r.URL.Query().Get("q") != "quantum" || r.URL.Query().Get("format") != "json" {
			http.Error(w, "unexpected request "+r.URL.String(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(
			w,
			`{"results":[{"title":"Q","url":"https://example.org/q","content":"snippet","engine":"duckduckgo"}]}`,
		)
	}))
	defer server.Close()

	results, backend, err := Search(context.Background(), "quantum", SearchOptions{SearXNGURL: server.URL})
	if err != nil || backend != "searxng" || len(results) != 1 || results[0].URL != "https://example.org/q" {
		t.Fatalf("Search(loopback SearXNG) = %+v, %q, %v; want one searxng result", results, backend, err)
	}
}

// Trust is the exact configured origin, never a redirect target: a SearXNG
// answering with a 3xx to an internal address must not walk the request off.
func TestSearchRefusesRedirectOffConfiguredSearXNG(t *testing.T) {
	var internalHits atomic.Int32
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		internalHits.Add(1)
		fmt.Fprint(w, `{"results":[]}`)
	}))
	defer internal.Close()
	searx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.URL+"/latest/meta-data", http.StatusFound)
	}))
	defer searx.Close()

	_, backend, err := Search(context.Background(), "q", SearchOptions{SearXNGURL: searx.URL})
	if err == nil || backend != "error" || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf(
			"Search(redirecting SearXNG) backend=%q err=%v; want an error naming the refused redirect",
			backend,
			err,
		)
	}
	if internalHits.Load() != 0 {
		t.Fatalf("redirect target was contacted %d time(s)", internalHits.Load())
	}
}

// A failed search reports WHAT failed for each backend — never a generic
// "unreachable or failing" that sends the operator to debug the wrong system.
func TestSearchFailureCarriesEachBackendError(t *testing.T) {
	searx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream down", http.StatusBadGateway)
	}))
	defer searx.Close()
	brave := &http.Client{Transport: searchRoundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Body:       io.NopCloser(strings.NewReader("{}")),
			Header:     http.Header{},
			Request:    r,
		}, nil
	})}

	_, backend, err := Search(
		context.Background(),
		"q",
		SearchOptions{SearXNGURL: searx.URL, BraveAPIKey: "k", Brave: brave},
	)
	if err == nil || backend != "error" {
		t.Fatalf("Search(both failing) backend=%q err=%v; want backend error", backend, err)
	}
	for _, want := range []string{"searxng", "HTTP 502", "brave", "HTTP 401"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("search error %q lacks %q", err.Error(), want)
		}
	}
}

// Disabled search is decided before any backend is contacted.
func TestDisabledSearchNeverContactsABackend(t *testing.T) {
	var hits atomic.Int32
	counting := &http.Client{Transport: searchRoundTrip(func(r *http.Request) (*http.Response, error) {
		hits.Add(1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"results":[]}`)),
			Header:     http.Header{},
			Request:    r,
		}, nil
	})}
	_, _, err := Search(context.Background(), "q", SearchOptions{
		SearXNGURL: "https://search.example.test", SearXNG: counting, DisableSearch: true,
	})
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("Search(disabled) err=%v; want the disabled refusal", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("disabled search contacted a backend %d time(s)", hits.Load())
	}
}

// Trusting the configured SearXNG origin for search must not open fetch to
// it: fetch URLs arrive from untrusted content and keep the full guard.
func TestTrustedSearXNGOriginDoesNotOpenFetch(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, "<html><body>internal</body></html>")
	}))
	defer server.Close()
	h, err := New(Options{CacheDir: t.TempDir(), SearXNGURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	result := h.Fetch(context.Background(), server.URL+"/admin")
	if result.Error == "" || hits.Load() != 0 {
		t.Fatalf(
			"fetch of the SearXNG origin: error=%q hits=%d; want a policy refusal before any request",
			result.Error,
			hits.Load(),
		)
	}
}
