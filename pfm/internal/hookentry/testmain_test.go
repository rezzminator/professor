package hookentry

import (
	"encoding/json"
	"os"
	"path/filepath"
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

// readHookentryData decodes testdata/{name}, a JSON file of the package's test inputs, into into.
// The jail moves the process, so the path starts at the module root.
func readHookentryData(t *testing.T, name string, into any) {
	t.Helper()
	path := filepath.Join(testjail.ModuleRoot(), "internal", "hookentry", "testdata", name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}
