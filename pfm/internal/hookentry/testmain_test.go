package hookentry

import (
	"os"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	claudeengine "github.com/rezzminator/professor/pfm/internal/engine/claude"
	codexengine "github.com/rezzminator/professor/pfm/internal/engine/codex"
	opencodeengine "github.com/rezzminator/professor/pfm/internal/engine/opencode"
	"github.com/rezzminator/professor/pfm/internal/index"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestMain(m *testing.M) {
	// The composition root (cmd/pfm/engines.go) wires the index sources in
	// production; a hook that refreshes the index needs them here too.
	index.RegisterSource(pfmengine.Claude, claudeengine.Source{})
	index.RegisterSource(pfmengine.Codex, codexengine.Source{})
	index.RegisterSource(pfmengine.OpenCode, opencodeengine.Source{})
	os.Exit(testjail.RunInJail(m))
}
