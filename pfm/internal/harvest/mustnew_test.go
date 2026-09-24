package harvest

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
)

// errFixturePublicResolve is what the default test resolver returns: a test
// harvester that reaches the public resolver has left its fixture.
var errFixturePublicResolve = errors.New("fixture: public resolve refused")

// mustNew constructs a Harvester or fails the test with New's own error.
// Tests that exercise cache-dir resolution itself call New directly.
//
// A test that sets no ResolvePublic gets publicResolveGuard: every production
// client New builds (an unset slot, a Chrome slot aliasing Client, an OA slot
// aliasing a custom Client) resolves through it, so a rung that would leave
// the fixture for real DNS/DoH, archive.org or a live host fails the test by
// name instead of silently waiting on the network.
func mustNew(t testing.TB, options Options) *Harvester {
	t.Helper()
	if options.ResolvePublic == nil {
		options.ResolvePublic = publicResolveGuard(t)
	}
	h, err := New(options)
	if err != nil {
		t.Fatalf("harvest.New: %v", err)
	}
	return h
}

// publicResolveGuard fails t for any public lookup, naming the host, and
// refuses it with errFixturePublicResolve.
func publicResolveGuard(t testing.TB) func(context.Context, string) ([]net.IP, error) {
	return func(_ context.Context, host string) ([]net.IP, error) {
		t.Errorf("a harvester client resolved %q through the public resolver; every rung must reach the fixture", host)
		return nil, errFixturePublicResolve
	}
}

// fixtureTwin is a second client over c's own transport. New treats a Chrome
// slot that is the same pointer as Client — or an OA slot aliasing a custom
// Client — as unset and swaps in a production client that dials the real
// network; a twin keeps that rung on the fixture the test meant it to reach.
func fixtureTwin(c *http.Client) *http.Client {
	return &http.Client{Transport: c.Transport, CheckRedirect: c.CheckRedirect}
}
