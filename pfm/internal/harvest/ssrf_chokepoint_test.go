package harvest

import (
	"errors"
	"net"
	"strings"
	"testing"
)

// TestAssertFetchableStrictFailsClosedOnDNSFailure pins the browser rung's
// SSRF posture: Chrome resolves on its own with no pinning hop, so a resolver
// failure inside the guard must REFUSE, never allow. The old code skipped the
// address loop when LookupIP errored and returned nil — allow — which let a
// TTL-0 rebind record pass validation and re-resolve to loopback inside the
// browser.
func TestAssertFetchableStrictFailsClosedOnDNSFailure(t *testing.T) {
	previous := lookupIP
	t.Cleanup(func() { lookupIP = previous })

	lookupIP = func(_ string) ([]net.IP, error) {
		return nil, errors.New("read udp 10.0.0.2:53: i/o timeout")
	}
	err := AssertFetchableStrict("https://rebind.attacker.test/page")
	if err == nil {
		t.Fatal("resolver failure was ALLOWED; the strict guard must fail closed")
	}
	if !strings.Contains(err.Error(), "DNS resolution failed") {
		t.Fatalf("refusal = %q, want a DNS-failure reason", err)
	}
}

// TestAssertFetchableStrictRefusesAnEmptyAnswer covers the resolver answering
// successfully but with no records: there is nothing validated, so there is
// nothing to allow.
func TestAssertFetchableStrictRefusesAnEmptyAnswer(t *testing.T) {
	previous := lookupIP
	t.Cleanup(func() { lookupIP = previous })

	lookupIP = func(_ string) ([]net.IP, error) { return nil, nil }
	if err := AssertFetchableStrict("https://empty.attacker.test/"); err == nil {
		t.Fatal("an empty DNS answer was ALLOWED")
	}
}

func TestAssertFetchableStrictRechecksDNSImmediatelyBeforeBrowserApproval(t *testing.T) {
	previous := lookupIP
	t.Cleanup(func() { lookupIP = previous })
	calls := 0
	lookupIP = func(_ string) ([]net.IP, error) {
		calls++
		if calls == 1 {
			return []net.IP{net.ParseIP("93.184.216.34")}, nil
		}
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	}
	if err := AssertFetchableStrict("https://rebind.attacker.test/page"); err == nil {
		t.Fatal("strict browser guard approved a host that rebound between its two immediate resolutions")
	}
	if calls != 2 {
		t.Fatalf("strict browser guard DNS calls=%d, want two immediate checks", calls)
	}
}

// TestAssertFetchableAppliesTheSuffixList pins the corollary: the per-redirect
// chokepoint must refuse the same internal suffixes IsPrivateHost refuses one
// hop earlier (.internal, .ts.net), or a 302 to an internal vault slips
// through.
func TestAssertFetchableAppliesTheSuffixList(t *testing.T) {
	for _, raw := range []string{
		"http://vault.internal/secret",
		"http://home.ts.net/admin",
	} {
		if err := AssertFetchable(raw); err == nil {
			t.Fatalf("%s was ALLOWED; internal suffixes must be refused", raw)
		}
		if err := AssertFetchableStrict(raw); err == nil {
			t.Fatalf("%s was ALLOWED by the strict guard; internal suffixes must be refused", raw)
		}
	}
}

// TestAssertFetchableAllowsPublicResolution is the control arm: a host that
// resolves to public addresses only passes BOTH guards.
func TestAssertFetchableAllowsPublicResolution(t *testing.T) {
	previous := lookupIP
	t.Cleanup(func() { lookupIP = previous })

	lookupIP = func(_ string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("93.184.216.34")}, nil
	}
	for _, guard := range []func(string) error{AssertFetchable, AssertFetchableStrict} {
		for _, raw := range []string{"https://example.com/", "https://public.example.test/"} {
			if err := guard(raw); err != nil {
				t.Fatalf("public host %s refused: %v", raw, err)
			}
		}
	}
}

// TestAssertFetchableStaysLenientOnResolverFailure documents the split: the
// HTTP-client rungs dial only addresses their own dialer validated
// (pinnedDialContext is that defense), so a resolver failure there stays
// lenient and fixture hosts that cannot resolve still pass validation.
func TestAssertFetchableStaysLenientOnResolverFailure(t *testing.T) {
	previous := lookupIP
	t.Cleanup(func() { lookupIP = previous })

	lookupIP = func(_ string) ([]net.IP, error) {
		return nil, errors.New("resolver down")
	}
	if err := AssertFetchable("https://fixture.example.test/"); err != nil {
		t.Fatalf("non-strict guard refused on resolver failure: %v", err)
	}
	if err := AssertFetchableStrict("https://fixture.example.test/"); err == nil {
		t.Fatal("strict guard allowed on resolver failure — the browser re-resolves unpinned")
	}
}
