package mcpserv

import (
	"io"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// newFixtureService is the package's protocol-test service over the real
// verb layer: chat.Verbs on the default runtime, which reads the jailed paths
// setupBackendFixture laid down.
func newFixtureService(t *testing.T) *Service {
	t.Helper()
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewConfigured("test", io.Discard, Runtime{
		Paths:                resolved,
		Chat:                 chat.Verbs{Warnings: io.Discard},
		AllowAmbientIdentity: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}
