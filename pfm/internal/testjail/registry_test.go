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
		{"flights-foreman-sonnet", "flights-foreman"}, {"reviewer-nt", "reviewer"},
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
	shared, err := os.ReadDir(filepath.Join(checkoutRoot(), "templates", "global", "agents", "shared"))
	if err != nil || len(shared) == 0 {
		t.Fatalf("read the shared files the registry must not list: %v (%d)", err, len(shared))
	}
	for _, entry := range entries {
		for _, file := range shared {
			if entry.Name() == file.Name() {
				t.Fatalf("shared file became an agent: %s", entry.Name())
			}
		}
	}
	// A second staging over the same home keeps the correct links.
	StageGlobalAgents(t, home)
}
