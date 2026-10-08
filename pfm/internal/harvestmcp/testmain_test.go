package harvestmcp

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/ask"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	claudeengine "github.com/rezzminator/professor/pfm/internal/engine/claude"
	codexengine "github.com/rezzminator/professor/pfm/internal/engine/codex"
	"github.com/rezzminator/professor/pfm/internal/harvest"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestMain jails the package and registers the two ask runners cmd/pfm's
// engines.go registers at startup, so read's ask resolves them here too.
func TestMain(m *testing.M) {
	restore := harvest.StubPublicResolverForTest(harvest.RefusePublicLookupsForTest)
	ask.RegisterRunner(pfmengine.Claude, claudeengine.AskRunner{})
	ask.RegisterRunner(pfmengine.Codex, codexengine.AskRunner{})
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
