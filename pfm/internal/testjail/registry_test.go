package testjail

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStageGlobalAgentsLinksTheSources(t *testing.T) {
	home := t.TempDir()
	StageGlobalAgents(t, home)
	link := filepath.Join(home, ".claude", "agents", "gitter.md")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("store agent link %s: %v", link, err)
	}
	if want := filepath.Join(checkoutRoot(), "templates", "global", "agents", "gitter.md"); target != want {
		t.Fatalf("%s -> %s, want %s", link, target, want)
	}
	// A second staging over the same home keeps the correct links.
	StageGlobalAgents(t, home)
}
