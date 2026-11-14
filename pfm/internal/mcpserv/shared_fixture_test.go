package mcpserv

import (
	"io"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
)

// newFixtureService is the package's protocol-test service over the real
// verb layer: chat.Verbs on the default runtime, which reads the jailed paths
// and opens the cache database setupBackendPaths names.
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

// memoryStore is the store a unit test hands its backend or Runtime: both
// databases in memory (store.OpenMemory), closed with the test.
func memoryStore(t *testing.T) *store.Store {
	t.Helper()
	database, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	return database
}
