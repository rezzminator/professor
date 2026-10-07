package testjail

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStageGlobalAgentsUsesTheStore(t *testing.T) {
	home := t.TempDir()
	StageGlobalAgents(t, home)
	entries, err := os.ReadDir(filepath.Join(home, ".claude", "agents"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("store agents=%v error=%v", entries, err)
	}
}
