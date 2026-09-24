package harvest

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestDOHResolver points a resolver at a local stub endpoint and a
// controllable fallback, so every assertion below runs without the network.
func newTestDOHResolver(endpoint string, fallback func(context.Context, string) ([]net.IP, error)) *dohResolver {
	return &dohResolver{
		endpoint: endpoint,
		client:   &http.Client{Timeout: 5 * time.Second},
		cache:    make(map[string]dohEntry),
		warned:   make(map[string]bool),
		fallback: fallback,
	}
}

func refusingFallback(t *testing.T) func(context.Context, string) ([]net.IP, error) {
	t.Helper()
	return func(_ context.Context, host string) ([]net.IP, error) {
		t.Errorf("system resolver was consulted for %s; the DoH answer should have been used", host)
		return nil, errors.New("fallback must not run")
	}
}

// TestDOHResolverPrefersTheHTTPSAnswer is the whole reason doh.go exists. A
// consumer ISP can answer a source host with its own block address, and no
// rung can tell that answer from a real one — every dial then lands on a block
// page and the failure reads as if the SOURCE refused us. Resolving over HTTPS
// takes the answer out of the network's hands.
func TestDOHResolverPrefersTheHTTPSAnswer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("type") != "A" {
			_, _ = w.Write([]byte(`{"Status":0,"Answer":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"Status":0,"Answer":[{"type":1,"TTL":300,"data":"198.51.100.7"}]}`))
	}))
	defer server.Close()

	resolver := newTestDOHResolver(server.URL, refusingFallback(t))
	ips, err := resolver.LookupIP(context.Background(), "mirror.example.com")
	if err != nil {
		t.Fatalf("LookupIP error = %v", err)
	}
	if len(ips) != 1 || ips[0].String() != "198.51.100.7" {
		t.Fatalf("LookupIP = %v, want the DoH answer 198.51.100.7", ips)
	}
}

// TestDOHResolverFallsBackWhenDoHCannotAnswer: a network that blocks DoH must
// still fetch. The fallback is deliberate, and the production path LOGS it —
// a silent downgrade to the rewritten resolver would report the block page's
// failures as the source's own.
func TestDOHResolverFallsBackWhenDoHCannotAnswer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "blocked", http.StatusTeapot)
	}))
	defer server.Close()

	called := false
	resolver := newTestDOHResolver(server.URL, func(context.Context, string) ([]net.IP, error) {
		called = true
		return []net.IP{net.ParseIP("203.0.113.9")}, nil
	})
	ips, err := resolver.LookupIP(context.Background(), "mirror.example.com")
	if err != nil {
		t.Fatalf("LookupIP error = %v, want the system-resolver fallback", err)
	}
	if !called || len(ips) != 1 || ips[0].String() != "203.0.113.9" {
		t.Fatalf("LookupIP = %v (fallback called: %v), want the fallback answer", ips, called)
	}
}

// TestDOHResolverReportsBothFailures: when DoH AND the system resolver both
// fail, the error must name both. Reporting only the second reads as "this host
// does not exist" when the real story may be "our resolver was unreachable" —
// an outage rendered as an absence.
func TestDOHResolverReportsBothFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "blocked", http.StatusTeapot)
	}))
	defer server.Close()

	resolver := newTestDOHResolver(server.URL, func(context.Context, string) ([]net.IP, error) {
		return nil, errors.New("system resolver unreachable")
	})
	_, err := resolver.LookupIP(context.Background(), "mirror.example.com")
	if err == nil {
		t.Fatal("LookupIP error = nil, want a failure naming both resolvers")
	}
	for _, want := range []string{"DoH lookup failed", "system resolver also failed", "system resolver unreachable"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("LookupIP error = %q, want it to name %q", err, want)
		}
	}
}

// TestDOHResolverSkipsSpecialUseNames: RFC 6761/6762 names are never in the
// public DNS, so querying a public resolver for one can only ever return
// NXDOMAIN. Asking anyway costs a round trip, emits a misleading degradation
// warning on every lookup, and drags local fixture hosts onto the network.
func TestDOHResolverSkipsSpecialUseNames(t *testing.T) {
	resolver := newTestDOHResolver(
		"http://doh.invalid/should-never-be-called",
		func(_ context.Context, _ string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("127.0.0.1")}, nil
		},
	)
	for _, host := range []string{"fixture.test", "thing.invalid", "printer.local", "a.example", "svc.internal", "localhost"} {
		ips, err := resolver.LookupIP(context.Background(), host)
		if err != nil || len(ips) != 1 || ips[0].String() != "127.0.0.1" {
			t.Fatalf("LookupIP(%q) = %v, %v; want the system resolver consulted directly", host, ips, err)
		}
	}
}

// TestDOHResolverTreatsNXDOMAINAsAuthoritative: Status 3 is a real DNS
// answer meaning the name does not exist, not a resolver outage. Falling
// back to the system resolver on NXDOMAIN — as query()'s own comment always
// said it should not — lets a poisoned system resolver override a correct
// "no such host" answer with its own block address, and warns about a
// failure that never happened.
func TestDOHResolverTreatsNXDOMAINAsAuthoritative(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"Status":3,"Answer":[]}`))
	}))
	defer server.Close()

	resolver := newTestDOHResolver(server.URL, refusingFallback(t))
	_, err := resolver.LookupIP(context.Background(), "nonexistent.example.com")
	if err == nil {
		t.Fatal("LookupIP error = nil, want a not-found error for an NXDOMAIN answer")
	}
	var dnsErr *net.DNSError
	if !errors.As(err, &dnsErr) || !dnsErr.IsNotFound {
		t.Fatalf("LookupIP error = %v (%#v), want a *net.DNSError with IsNotFound = true", err, err)
	}
}

// TestDOHResolverCachesWithinTTL keeps a burst of rungs against one host from
// re-querying the resolver for every dial.
func TestDOHResolverCachesWithinTTL(t *testing.T) {
	queries := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries++
		if r.URL.Query().Get("type") != "A" {
			_, _ = w.Write([]byte(`{"Status":0,"Answer":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"Status":0,"Answer":[{"type":1,"TTL":300,"data":"198.51.100.7"}]}`))
	}))
	defer server.Close()

	resolver := newTestDOHResolver(server.URL, refusingFallback(t))
	for i := 0; i < 3; i++ {
		if _, err := resolver.LookupIP(context.Background(), "mirror.example.com"); err != nil {
			t.Fatalf("LookupIP #%d error = %v", i, err)
		}
	}
	// One A + one AAAA query for the first lookup; the rest come from cache.
	if queries > 2 {
		t.Fatalf("resolver queries = %d, want the TTL cache to serve repeat lookups", queries)
	}
}

// TestDOHResolverRequeriesAfterTTLExpires is the other half of the TTL
// contract: TestDOHResolverCachesWithinTTL alone never proves expiry, since a
// resolver that cached forever would pass it too. An entry already past its
// expires time must be re-queried, not served stale.
func TestDOHResolverRequeriesAfterTTLExpires(t *testing.T) {
	queries := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries++
		if r.URL.Query().Get("type") != "A" {
			_, _ = w.Write([]byte(`{"Status":0,"Answer":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"Status":0,"Answer":[{"type":1,"TTL":300,"data":"198.51.100.7"}]}`))
	}))
	defer server.Close()

	resolver := newTestDOHResolver(server.URL, refusingFallback(t))
	resolver.store("mirror.example.com", []net.IP{net.ParseIP("203.0.113.99")}, dohMinTTL)
	resolver.mu.Lock()
	expired := resolver.cache["mirror.example.com"]
	expired.expires = time.Now().Add(-time.Second)
	resolver.cache["mirror.example.com"] = expired
	resolver.mu.Unlock()

	ips, err := resolver.LookupIP(context.Background(), "mirror.example.com")
	if err != nil {
		t.Fatalf("LookupIP error = %v", err)
	}
	if queries == 0 {
		t.Fatal("resolver queries = 0, want an expired cache entry to trigger a fresh query")
	}
	if len(ips) != 1 || ips[0].String() != "198.51.100.7" {
		t.Fatalf("LookupIP = %v, want the freshly queried answer, not the expired cached one", ips)
	}
}

// TestNewInstallsTheDoHResolverByDefault pins the wiring. Every harvester
// client is built with Options.ResolvePublic, so this default is the ONE place
// that moves all six transports off a resolver the network can rewrite; a nil
// here would silently return every rung to the poisoned answer.
func TestNewInstallsTheDoHResolverByDefault(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	// Opt-out of mustNew's publicResolveGuard: that default would fill the
	// very field this test pins. Constructing resolves nothing.
	h, err := New(Options{CacheDir: t.TempDir(), Converter: &fakeConverter{}})
	if err != nil {
		t.Fatalf("harvest.New: %v", err)
	}
	if h.options.ResolvePublic == nil {
		t.Fatal("Options.ResolvePublic = nil after New; want the DoH resolver installed by default")
	}
}

// TestNewKeepsAnInjectedResolver: the DNS-rebinding tests depend on this seam,
// and an operator supplying their own resolver must keep it.
func TestNewKeepsAnInjectedResolver(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	marker := errors.New("injected resolver ran")
	h := mustNew(t, Options{
		CacheDir:      t.TempDir(),
		Converter:     &fakeConverter{},
		ResolvePublic: func(context.Context, string) ([]net.IP, error) { return nil, marker },
	})
	if _, err := h.options.ResolvePublic(context.Background(), "example.com"); !errors.Is(err, marker) {
		t.Fatalf("injected resolver was replaced; err = %v", err)
	}
}

// TestBrowserHostResolverRulePinsTheDoHAnswer closes the gap the HTTP rungs do
// not cover: Chrome resolves DNS itself, with no pinning hop. Without an
// explicit rule the browser rung would re-resolve through the very resolver DoH
// exists to bypass — so on a rewriting network every HTTP rung would reach the
// real host while the browser rung alone landed on a block page, and the wall
// would read as the source's own refusal.
func TestBrowserHostResolverRulePinsTheDoHAnswer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("type") != "A" {
			_, _ = w.Write([]byte(`{"Status":0,"Answer":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"Status":0,"Answer":[{"type":1,"TTL":300,"data":"198.51.100.7"}]}`))
	}))
	defer server.Close()

	resolver := newTestDOHResolver(server.URL, refusingFallback(t))
	ips, err := resolver.LookupIP(context.Background(), "mirror.example.com")
	if err != nil || len(ips) == 0 {
		t.Fatalf("LookupIP = %v, %v", ips, err)
	}
	if rule := browserHostResolverRuleFrom(
		"https://mirror.example.com/doc",
		ips,
	); rule != "MAP mirror.example.com 198.51.100.7" {
		t.Fatalf("rule = %q, want Chrome pinned to the DoH answer", rule)
	}
}

// TestBrowserHostResolverRuleRefusesPrivateAndUnpinnable: a literal IP needs no
// rule, and a private answer must never be pinned — pinning one would hand
// Chrome an internal address the SSRF guard exists to refuse.
func TestBrowserHostResolverRuleRefusesPrivateAndUnpinnable(t *testing.T) {
	for name, tc := range map[string]struct {
		url string
		ips []net.IP
	}{
		"literal ip":       {"https://198.51.100.7/doc", []net.IP{net.ParseIP("198.51.100.7")}},
		"special-use name": {"https://fixture.test/doc", []net.IP{net.ParseIP("198.51.100.7")}},
		"private answer":   {"https://mirror.example.com/doc", []net.IP{net.ParseIP("10.0.0.5")}},
		"no answer":        {"https://mirror.example.com/doc", nil},
	} {
		t.Run(name, func(t *testing.T) {
			if rule := browserHostResolverRuleFrom(tc.url, tc.ips); rule != "" {
				t.Fatalf("rule = %q, want no pin", rule)
			}
		})
	}
}

// TestBrowserHostResolverRulePrefersIPv4: a rule pins Chrome to ONE address and
// the browser rung has no multi-address fallback of its own. The resolved set is
// string-sorted, so without an explicit preference an IPv6 address could win the
// pin on lexical order alone and strand the browser rung on an unreachable route
// while every HTTP rung succeeds over IPv4.
func TestBrowserHostResolverRulePrefersIPv4(t *testing.T) {
	ips := []net.IP{net.ParseIP("2001:db8::1"), net.ParseIP("198.51.100.7")}
	if rule := browserHostResolverRuleFrom(
		"https://mirror.example.com/doc",
		ips,
	); rule != "MAP mirror.example.com 198.51.100.7" {
		t.Fatalf("rule = %q, want the IPv4 address pinned", rule)
	}
	only6 := []net.IP{net.ParseIP("2001:db8::1")}
	if rule := browserHostResolverRuleFrom(
		"https://mirror.example.com/doc",
		only6,
	); rule != "MAP mirror.example.com 2001:db8::1" {
		t.Fatalf("rule = %q, want the IPv6 address when it is the only one", rule)
	}
}
