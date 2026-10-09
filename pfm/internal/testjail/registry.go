package testjail

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// StageGlobalAgents links the checkout's machine-global agent sources into
// {home}/.claude/agents, the Claude half of what pfm install's engine-sync
// global step wires; a jail reading a role by name finds it there. The
// compiled Codex roles are engine-sync's, proven in its own gate.
func StageGlobalAgents(t *testing.T, home string) {
	t.Helper()
	source := filepath.Join(checkoutRoot(), "templates", "global", "agents")
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatalf("read the global agent sources %s: %v", source, err)
	}
	registry := filepath.Join(home, ".claude", "agents")
	if err := os.MkdirAll(registry, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		target, link := filepath.Join(source, entry.Name()), filepath.Join(registry, entry.Name())
		if found, err := os.Readlink(link); err == nil && found == target {
			continue
		}
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
}
