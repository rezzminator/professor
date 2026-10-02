package harvest

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestMain(m *testing.M) {
	restore := StubPublicResolverForTest(RefusePublicLookupsForTest)
	code := testjail.Run(m)
	restore()
	os.Exit(code)
}

// TestMainStubsThePublicResolver: a public lookup answers with the offline
// resolver's refusal, so no test reaches the real DNS-over-HTTPS path. Serial
// on purpose: the seam tests swap the stub, and a parallel test would race them.
func TestMainStubsThePublicResolver(t *testing.T) {
	ips, err := ResolvePublicHost(context.Background(), "stub-kept.doh-seam.net")
	var dnsErr *net.DNSError
	if len(ips) != 0 || !errors.As(err, &dnsErr) || dnsErr.Err != "public lookup refused in tests" {
		t.Fatalf("ResolvePublicHost = %v, %v; want the refusal TestMain's stub answers with", ips, err)
	}
}
