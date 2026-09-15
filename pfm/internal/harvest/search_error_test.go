package harvest

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSearchHintNamesSearchOnlyWhenAvailable pins the one shared helper every
// "use `search`" message routes through.
func TestSearchHintNamesSearchOnlyWhenAvailable(t *testing.T) {
	if got := SearchHint(true, "with", "without"); got != "with" {
		t.Fatalf("SearchHint(true) = %q, want %q", got, "with")
	}
	if got := SearchHint(false, "with", "without"); got != "without" {
		t.Fatalf("SearchHint(false) = %q, want %q", got, "without")
	}
}

// TestFailureMessageNamesSearchOnlyWhenAvailable is the regression for a
// failure message recommending a `search` tool that a caller with no
// configured backend cannot possibly use — failureMessage used to name
// `search` unconditionally in every one of these branches.
func TestFailureMessageNamesSearchOnlyWhenAvailable(t *testing.T) {
	for _, kind := range []string{"invalid", "timeout", "dns", "connect"} {
		on := failureMessage("https://example.test/x", 0, kind, false, true)
		if !strings.Contains(on, "`search`") {
			t.Errorf("failureMessage(%s, search on) = %q, want it to name `search`", kind, on)
		}
		off := failureMessage("https://example.test/x", 0, kind, false, false)
		if strings.Contains(off, "`search`") {
			t.Errorf("failureMessage(%s, search off) = %q, unconditionally names `search`", kind, off)
		}
	}

	// The challenge branch, the HTTP-status branch, and the final unclassified
	// fallback each carry their own "use `search`" clause — regression for the
	// three net.go branches that used to name it unconditionally.
	on := failureMessage("https://example.test/x", 0, "", true, true)
	if !strings.Contains(on, "`search`") {
		t.Errorf("failureMessage(challenge, search on) = %q, want it to name `search`", on)
	}
	off := failureMessage("https://example.test/x", 0, "", true, false)
	if strings.Contains(off, "`search`") {
		t.Errorf("failureMessage(challenge, search off) = %q, unconditionally names `search`", off)
	}

	on = failureMessage("https://example.test/x", 404, "", false, true)
	if !strings.Contains(on, "`search`") {
		t.Errorf("failureMessage(HTTP 404, search on) = %q, want it to name `search`", on)
	}
	off = failureMessage("https://example.test/x", 404, "", false, false)
	if strings.Contains(off, "`search`") {
		t.Errorf("failureMessage(HTTP 404, search off) = %q, unconditionally names `search`", off)
	}

	on = failureMessage("https://example.test/x", 0, "", false, true)
	if !strings.Contains(on, "`search`") {
		t.Errorf("failureMessage(fallback, search on) = %q, want it to name `search`", on)
	}
	off = failureMessage("https://example.test/x", 0, "", false, false)
	if strings.Contains(off, "`search`") {
		t.Errorf("failureMessage(fallback, search off) = %q, unconditionally names `search`", off)
	}
}

// TestNewCarriesSearchAvailableIntoSettings pins Options.SearchAvailable
// reaching the ladder's own settings, the one place its failure messages
// read the flag from.
func TestNewCarriesSearchAvailableIntoSettings(t *testing.T) {
	on := mustNew(t, Options{CacheDir: t.TempDir(), SearchAvailable: true})
	if !on.settings.searchAvailable {
		t.Fatal("New(SearchAvailable: true) did not carry into settings.searchAvailable")
	}
	off := mustNew(t, Options{CacheDir: t.TempDir(), SearchAvailable: false})
	if off.settings.searchAvailable {
		t.Fatal("New(SearchAvailable: false) reported settings.searchAvailable = true")
	}
}

// TestSearchConfigurationErrorsAreSentinels pins the two configuration
// failures a caller must be able to errors.Is against, distinct from a
// backend outage that might clear on retry.
func TestSearchConfigurationErrorsAreSentinels(t *testing.T) {
	_, _, err := Search(context.Background(), "q", SearchOptions{DisableSearch: true})
	if !errors.Is(err, ErrSearchDisabled) {
		t.Fatalf("Search(disabled) = %v, want errors.Is ErrSearchDisabled", err)
	}
	_, _, err = Search(context.Background(), "q", SearchOptions{})
	if !errors.Is(err, ErrSearchNotConfigured) {
		t.Fatalf("Search(unconfigured) = %v, want errors.Is ErrSearchNotConfigured", err)
	}
}

// TestSearchBackendErrorNamesBackendAndSafeCause pins backendCause's fixed,
// safe vocabulary: it never echoes a raw error string (which could carry the
// operator's URL), retry is said only for a timeout or a 5xx, and SearXNG's
// 403 carries its own json-format hint.
// TestProbeSearchReportsEveryState pins doctor's four search states: OFF is
// named, not a warning; a reachable SearXNG /healthz names the json-format
// caveat; an unreachable one is a warning; a configured Brave key is reported
// without ever being probed (a probe would spend the operator's quota).
func TestProbeSearchReportsEveryState(t *testing.T) {
	t.Run("off disabled", func(t *testing.T) {
		probe := ProbeSearch(
			context.Background(),
			SearchOptions{SearXNGURL: "https://search.example.test", DisableSearch: true},
			nil,
		)
		if probe.State != SearchProbeOff || probe.Warning {
			t.Fatalf("ProbeSearch(disabled) = %+v, want OFF and no warning", probe)
		}
	})
	t.Run("off unconfigured", func(t *testing.T) {
		probe := ProbeSearch(context.Background(), SearchOptions{}, nil)
		if probe.State != SearchProbeOff || probe.Warning {
			t.Fatalf("ProbeSearch(unconfigured) = %+v, want OFF and no warning", probe)
		}
	})
	t.Run("searxng reachable", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/healthz" {
				http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()
		probe := ProbeSearch(context.Background(), SearchOptions{SearXNGURL: server.URL}, nil)
		if probe.State != SearchProbeReachable || probe.Warning || probe.Backend != "searxng" {
			t.Fatalf("ProbeSearch(reachable) = %+v, want reachable/searxng, no warning", probe)
		}
		if !strings.Contains(probe.Detail, "json format is not probed") {
			t.Fatalf("ProbeSearch(reachable).Detail = %q, want the json-format caveat", probe.Detail)
		}
	})
	t.Run("searxng unreachable", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		closedURL := server.URL
		server.Close()
		probe := ProbeSearch(context.Background(), SearchOptions{SearXNGURL: closedURL}, nil)
		if probe.State != SearchProbeUnreachable || !probe.Warning || probe.Backend != "searxng" {
			t.Fatalf("ProbeSearch(unreachable) = %+v, want UNREACHABLE/searxng warning", probe)
		}
	})
	t.Run("brave configured not probed", func(t *testing.T) {
		var hit bool
		probe := ProbeSearch(
			context.Background(),
			SearchOptions{BraveAPIKey: "example-fixture-key"},
			&http.Client{Transport: searchRoundTrip(func(*http.Request) (*http.Response, error) {
				hit = true
				t.Fatal("ProbeSearch dialed Brave — a doctor probe must never spend the operator's quota")
				return nil, nil
			})},
		)
		if probe.State != SearchProbeConfigured || probe.Warning || probe.Backend != "brave" {
			t.Fatalf("ProbeSearch(brave key) = %+v, want configured/brave, no warning", probe)
		}
		if hit {
			t.Fatal("ProbeSearch contacted Brave")
		}
	})
}

func TestSearchBackendErrorNamesBackendAndSafeCause(t *testing.T) {
	tests := []struct {
		name    string
		backend string
		status  int
		err     error
		want    []string
		never   []string
	}{
		{"timeout retryable", "searxng", 0, context.DeadlineExceeded, []string{"searxng", "timed out"}, nil},
		{"5xx retryable", "searxng", 503, errors.New("HTTP 503"), []string{"searxng", "503"}, nil},
		{
			"403 searxng json hint",
			"searxng",
			403,
			errors.New("HTTP 403"),
			[]string{"searxng", "403", "settings.yml", "json format"},
			[]string{"retry"},
		},
		{
			"other status no retry wording",
			"brave",
			401,
			errors.New("HTTP 401"),
			[]string{"brave", "401"},
			[]string{"retry"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := newBackendError(test.backend, test.status, test.err).SafeMessage()
			for _, want := range test.want {
				if !strings.Contains(got, want) {
					t.Errorf("newBackendError(%s) = %q, missing %q", test.name, got, want)
				}
			}
			for _, never := range test.never {
				if strings.Contains(got, never) {
					t.Errorf("newBackendError(%s) = %q, must not contain %q", test.name, got, never)
				}
			}
		})
	}
}

// TestSearchBraveRefusesOversizeBodyByName: searchBrave previously ran
// through getBodyWithHeaders (oversizeTruncate: true), so an over-ceiling
// Brave response was silently truncated and then failed JSON decode with
// "unexpected end of JSON input" — reading as a malformed Brave response
// when the real story is the byte ceiling.
func TestSearchBraveRefusesOversizeBodyByName(t *testing.T) {
	withPublicDNSForProviderTest(t)
	oversize := strings.Repeat("a", 10*1024*1024+1<<20)
	brave := &http.Client{Transport: searchRoundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"web":{"results":[{"URL":"` + oversize + `"}]}}`)),
			Header:     http.Header{"Content-Type": {"application/json"}},
			Request:    r,
		}, nil
	})}
	_, status, err := searchBrave(context.Background(), "q", SearchOptions{BraveAPIKey: "k", Brave: brave, Count: 1})
	if err == nil {
		t.Fatal("searchBrave error = nil, want an oversize refusal")
	}
	if strings.Contains(err.Error(), "unexpected end of JSON input") {
		t.Fatalf("searchBrave error = %q, want it to name the byte ceiling rather than a decode failure", err)
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("searchBrave error = %q, want it to name the byte ceiling", err)
	}
	_ = status
}
