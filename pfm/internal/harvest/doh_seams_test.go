package harvest

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// TestAssertFetchableConsultsTheDoHResolver pins the fix for net.go:335 (spec
// doh-seams-spec.md, finding A): the SSRF/rebind pre-check that runs on every
// gateway request (validateFetchURL) must consult the SAME resolver every dial
// pins to (ResolvePublicHost / sharedDOHResolver), not the system resolver a
// rewriting network can answer with an RFC1918 sinkhole. This test never
// stubs lookupIP — the whole point is to prove validateFetchURL is wired to
// the DoH resolver's answer, not to fake the wiring by stubbing the seam
// directly. It uses the same test double doh_test.go uses
// (newTestDOHResolver + refusingFallback) and installs it behind the
// package's process-wide singleton var (sharedDOHResolver), which is safe to
// reassign here: sync.OnceValue's memoization lives in the closure a fresh
// sync.OnceValue(...) call creates, not in the package variable, so
// reassigning it always takes effect regardless of whether the previous
// instance was ever invoked. The fixture host is NOT under an RFC 6761
// special-use suffix (.example, .test, .invalid …): doh.go answers those from
// the fallback without ever querying DoH, which is exactly the path this test
// must not take.
func TestAssertFetchableConsultsTheDoHResolver(t *testing.T) {
	previousResolver := sharedDOHResolver
	t.Cleanup(func() { sharedDOHResolver = previousResolver })

	sinkhole := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("type") != "A" {
			_, _ = w.Write([]byte(`{"Status":0,"Answer":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"Status":0,"Answer":[{"type":1,"TTL":300,"data":"10.0.0.1"}]}`))
	}))
	defer sinkhole.Close()

	sharedDOHResolver = sync.OnceValue(func() *dohResolver {
		return newTestDOHResolver(sinkhole.URL, refusingFallback(t))
	})
	if err := AssertFetchable(
		"https://sinkhole.doh-seam.net/",
	); err == nil ||
		!strings.Contains(err.Error(), "private") {
		t.Fatalf(
			"AssertFetchable(sinkhole.doh-seam.net) = %v, want a refusal naming the DoH-answered private address 10.0.0.1",
			err,
		)
	}

	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("type") != "A" {
			_, _ = w.Write([]byte(`{"Status":0,"Answer":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"Status":0,"Answer":[{"type":1,"TTL":300,"data":"93.184.216.34"}]}`))
	}))
	defer public.Close()

	sharedDOHResolver = sync.OnceValue(func() *dohResolver {
		return newTestDOHResolver(public.URL, refusingFallback(t))
	})
	if err := AssertFetchable("https://sinkhole.doh-seam.net/"); err != nil {
		t.Fatalf("AssertFetchable(sinkhole.doh-seam.net) with a public DoH answer = %v, want nil", err)
	}
}
