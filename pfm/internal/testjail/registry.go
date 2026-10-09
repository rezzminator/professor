package testjail

import (
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/codexgen"
)

// StageGlobalAgents wires the checkout's current role roster into a doctor jail.
func StageGlobalAgents(t *testing.T, home string) {
	t.Helper()
	_, err := codexgen.RunGlobalAgents(codexgen.GlobalAgentsOptions{
		Home: home, SourceRepo: checkoutRoot(),
		ClaudeConfigDirs: []string{filepath.Join(home, ".claude")},
		CodexHomes:       []string{filepath.Join(home, ".codex")}, Mode: codexgen.ModeBuild,
	})
	if err != nil {
		t.Fatal(err)
	}
}
