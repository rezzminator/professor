package harvestcli

import (
	"os"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/ask"
	"github.com/rezzminator/professor/pfm/internal/harvest"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	claudeengine "github.com/rezzminator/professor/pfm/internal/engine/claude"
	codexengine "github.com/rezzminator/professor/pfm/internal/engine/codex"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestMain jails the package and registers the two ask runners cmd/pfm's
// engines.go registers at startup, so `harvest ask` resolves them here too.
func TestMain(m *testing.M) {
	restore := harvest.StubPublicResolverForTest(harvest.RefusePublicLookupsForTest)
	ask.RegisterRunner(pfmengine.Claude, claudeengine.AskRunner{})
	ask.RegisterRunner(pfmengine.Codex, codexengine.AskRunner{})
	code := testjail.Run(m)
	restore()
	os.Exit(code)
}
