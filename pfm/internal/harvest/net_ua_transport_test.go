package harvest

import (
	"net/http"
	"testing"
)

// TestUserAgentTransportSetsUAAndForwardsToBase pins the one thing this
// wrapper exists to do: stamp the configured User-Agent onto a CLONE of the
// request (never mutate the caller's own *http.Request) and hand it to the
// wrapped transport. This is the forwarding call
// (net_ua_transport.go:RoundTrip -> t.base.RoundTrip) the gateway chokepoint
// test recognizes as transport-internal, below the gateway.
func TestUserAgentTransportSetsUAAndForwardsToBase(t *testing.T) {
	var seenUA string
	var sameRequest bool
	base := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		seenUA = r.Header.Get("User-Agent")
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: http.NoBody, Request: r}, nil
	})
	transport := &userAgentTransport{base: base, ua: "harvester-test/1.0"}
	req, err := http.NewRequest(http.MethodGet, "https://example.test/doc", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("User-Agent", "caller-supplied/0.0")
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("RoundTrip status = %d, want 200", resp.StatusCode)
	}
	if seenUA != "harvester-test/1.0" {
		t.Fatalf("base transport saw User-Agent = %q, want the wrapper's own %q", seenUA, "harvester-test/1.0")
	}
	if req.Header.Get("User-Agent") != "caller-supplied/0.0" {
		t.Fatalf(
			"RoundTrip mutated the caller's own request; User-Agent = %q, want the original untouched",
			req.Header.Get("User-Agent"),
		)
	}
	sameRequest = req.Header.Get("User-Agent") == "caller-supplied/0.0"
	if !sameRequest {
		t.Fatal("expected the original request to be left alone")
	}
}

// TestUserAgentTransportRefusesPrivateHostBeforeForwarding: RoundTrip
// re-validates every request through assertFetchable before it ever reaches
// the wrapped transport — the SSRF guard the gateway relies on holds even at
// this innermost layer, not only at gatewayAttempt's own check.
func TestUserAgentTransportRefusesPrivateHostBeforeForwarding(t *testing.T) {
	called := false
	base := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		called = true
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: http.NoBody, Request: r}, nil
	})
	transport := &userAgentTransport{base: base, ua: "harvester-test/1.0"}
	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:9/secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.RoundTrip(req); err == nil {
		t.Fatal("RoundTrip(private host) error = nil, want the SSRF guard to refuse it")
	}
	if called {
		t.Fatal("RoundTrip forwarded a private-host request to the base transport")
	}
}

// TestUserAgentTransportChromeAddsFingerprintHeaders pins the chrome-profile
// header set the Chrome-impersonation rung depends on — only added when
// chrome is true, never for the plain UA wrapper.
func TestUserAgentTransportChromeAddsFingerprintHeaders(t *testing.T) {
	var seen http.Header
	base := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		seen = r.Header
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: http.NoBody, Request: r}, nil
	})
	req, err := http.NewRequest(http.MethodGet, "https://example.test/doc", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&userAgentTransport{base: base, ua: chromeUA, chrome: true}).RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip error = %v", err)
	}
	if seen.Get("Sec-CH-UA") == "" || seen.Get("Sec-Fetch-Mode") != "navigate" {
		t.Fatalf("chrome=true request headers = %v, want the Chrome fingerprint headers set", seen)
	}

	seen = nil
	req2, err := http.NewRequest(http.MethodGet, "https://example.test/doc", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&userAgentTransport{base: base, ua: defaultUA}).RoundTrip(req2); err != nil {
		t.Fatalf("RoundTrip error = %v", err)
	}
	if seen.Get("Sec-CH-UA") != "" {
		t.Fatalf("chrome=false request carried a Chrome fingerprint header: %v", seen)
	}
}
