package harvestpy

import (
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
