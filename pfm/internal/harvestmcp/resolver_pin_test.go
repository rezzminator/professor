package harvestmcp

import (
	"net/http"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/harvest"
)

// TestResolverClientIsPinned pins newHTTPClient (spec doh-seams-spec.md,
// change B.2): the MCP Resolver's client must be built on harvest's pinned
// (DoH-resolved, SSRF-checked) transport instead of a bare
// http.DefaultTransport clone, and the runtime User-Agent the caller-facing
// wrapper sets must still reach the wire. The pinned-transport identity is
// asserted through harvest's own exported probe rather than reaching into
// harvest's unexported transport types from this package.
func TestResolverClientIsPinned(t *testing.T) {
	client, err := newHTTPClient(Runtime{UserAgent: "ua-x"})
	if err != nil {
		t.Fatal(err)
	}
	outer, ok := client.Transport.(userAgentTransport)
	if !ok {
		t.Fatalf("client.Transport = %T, want the MCP User-Agent wrapper", client.Transport)
	}
	if outer.value != "ua-x" {
		t.Fatalf("outer User-Agent = %q, want ua-x", outer.value)
	}
	inner := &http.Client{Transport: outer.base}
	if !harvest.IsPinnedClient(inner) {
		t.Fatal(
			"newHTTPClient's base transport is not harvest's pinned (DoH-resolved) client; want it built via harvest.NewDirectClient",
		)
	}
}
