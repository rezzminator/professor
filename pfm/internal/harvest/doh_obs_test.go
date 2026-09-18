package harvest

import (
	"testing"

	"hostops/pfm/internal/obs"
)

// TestNewDOHResolverWrapsItsClient proves newDOHResolver's client is wrapped
// with obs.WrapClient (item 11). newDOHResolver's Transport.DialContext dials
// only the literal Cloudflare bootstrap IPs (never a caller-supplied host), so
// there is no local injection point for a live round trip here — the
// identity check obs/httpout_test.go's TestWrapClientKeepsEveryPolicyByIdentityAndWrapsOnce
// pins is the proof this site can take: wrapping an already-wrapped client
// returns the exact same Transport, never a second layer.
func TestNewDOHResolverWrapsItsClient(t *testing.T) {
	resolver := newDOHResolver()
	rewrapped := obs.WrapClient(resolver.client)
	if rewrapped.Transport != resolver.client.Transport {
		t.Fatalf("newDOHResolver's client was not already wrapped: re-wrapping produced a different Transport")
	}
}
