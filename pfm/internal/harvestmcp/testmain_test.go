package harvestmcp

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/harvest"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestMain(m *testing.M) {
	restore := harvest.StubPublicResolverForTest(harvest.RefusePublicLookupsForTest)
	code := testjail.Run(m)
	restore()
	os.Exit(code)
}

// TestMainStubsThePublicResolver: a public lookup answers with the offline
// resolver's refusal, so no test reaches the real DNS-over-HTTPS path. Serial
// on purpose: it reads the one process-wide stub, which no parallel test should
// race.
func TestMainStubsThePublicResolver(t *testing.T) {
	ips, err := harvest.ResolvePublicHost(context.Background(), "stub-kept.doh-seam.net")
	var dnsErr *net.DNSError
	if len(ips) != 0 || !errors.As(err, &dnsErr) || dnsErr.Err != "public lookup refused in tests" {
		t.Fatalf("ResolvePublicHost = %v, %v; want the refusal TestMain's stub answers with", ips, err)
	}
}
