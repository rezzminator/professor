package harvest

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"
)

// TestNewDirectClientDialsThroughTheResolver pins the constructor spec
// doh-seams-spec.md (change B.1) requires: NewDirectClient's dial path must
// use exactly the resolve func it was given, not the system resolver.
// lookupIP (the package-level SSRF pre-check seam, unrelated to this
// client's own resolve param) is stubbed to a trivial public answer so the
// pre-check never leaves the process; only NewDirectClient's own dial path
// — pinnedDialContext's resolve — is under test here.
func TestNewDirectClientDialsThroughTheResolver(t *testing.T) {
	previous := lookupIP
	t.Cleanup(func() { lookupIP = previous })
	lookupIP = func(string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("93.184.216.34")}, nil
	}

	calls := 0
	var gotHost string
	recErr := errors.New("recorder refused to resolve")
	resolve := func(_ context.Context, host string) ([]net.IP, error) {
		calls++
		gotHost = host
		return nil, recErr
	}

	client := NewDirectClient(2*time.Second, nil, "", resolve)
	_, err := client.Get("https://example.test/")
	if err == nil {
		t.Fatal("Get succeeded; want the recorder's resolver error")
	}
	if !errors.Is(err, recErr) {
		t.Fatalf("Get error = %v, want it to wrap the recorder's error %v", err, recErr)
	}
	if calls != 1 {
		t.Fatalf("resolver was called %d times for example.test, want exactly 1", calls)
	}
	if gotHost != "example.test" {
		t.Fatalf("resolver host = %q, want example.test", gotHost)
	}
}

// TestNewDirectClientStampsTheUserAgentOnTheWire pins the UA precedence the
// spec's B.2 asked for: an adapter that wraps NewDirectClient's transport with
// its own User-Agent wrapper does not decide the wire header — harvest's inner
// wrapper stamps last. So the UA MUST enter through NewDirectClient itself; an
// empty ua keeps the harvester default. The wire is observed by swapping the
// inner wrapper's base for a recorder, with the SSRF pre-check's resolver seam
// stubbed public so the request never leaves the process.
func TestNewDirectClientStampsTheUserAgentOnTheWire(t *testing.T) {
	previous := lookupIP
	t.Cleanup(func() { lookupIP = previous })
	lookupIP = func(string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("93.184.216.34")}, nil
	}
	for _, tc := range []struct{ ua, want string }{
		{ua: "adapter-ua/2.0", want: "adapter-ua/2.0"},
		{ua: "", want: defaultUA},
	} {
		client := NewDirectClient(2*time.Second, nil, tc.ua, nil)
		wrapped, ok := client.Transport.(*userAgentTransport)
		if !ok {
			t.Fatalf("NewDirectClient transport = %T, want *userAgentTransport", client.Transport)
		}
		var seenUA string
		wrapped.base = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			seenUA = r.Header.Get("User-Agent")
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: http.NoBody, Request: r}, nil
		})
		// An outer wrapper stamping its own value, the way harvestmcp does.
		outer := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			clone := r.Clone(r.Context())
			clone.Header.Set("User-Agent", "outer-wrapper/0.0")
			return client.Transport.RoundTrip(clone)
		})}
		resp, err := outer.Get("https://example.test/")
		if err != nil {
			t.Fatalf("ua %q: Get error = %v", tc.ua, err)
		}
		if err := resp.Body.Close(); err != nil {
			t.Fatal(err)
		}
		if seenUA != tc.want {
			t.Fatalf("ua %q: wire User-Agent = %q, want %q", tc.ua, seenUA, tc.want)
		}
	}
}
