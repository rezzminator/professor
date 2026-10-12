package testjail

import (
	"os"
	"path/filepath"
	"strings"
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
	// A declaration is staged as a rendered-agent stand-in, not a raw Claude source.
	for _, row := range []struct{ name, role string }{
		{"gpt-foreman", "flights-foreman"}, {"gpt-reviewer", "reviewer"},
	} {
		data, err := os.ReadFile(filepath.Join(home, ".claude", "agents", row.name+".md"))
		if err != nil || !strings.Contains(string(data), "role: "+row.role+"\n") {
			t.Fatalf("rendered fixture %s role %s: %s (%v)", row.name, row.role, data, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(home, ".claude", "agents"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() == "gpt.md" || entry.Name() == "gpt-flights.md" || entry.Name() == "gpt-worker.md" ||
			entry.Name() == "repo-lane.md" {
			t.Fatalf("abstract source became an agent: %s", entry.Name())
		}
	}
	// A second staging over the same home keeps the correct links.
	StageGlobalAgents(t, home)
}
