package harvestmcp

import (
	"os"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/harvest"
)

func TestMain(m *testing.M) {
	restore := harvest.StubPublicResolverForTest(harvest.RefusePublicLookupsForTest)
	code := m.Run()
	restore()
	os.Exit(code)
}
