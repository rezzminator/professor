package professor

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSourceRepoFixture(t *testing.T, root string, files ...string) {
	t.Helper()
	for _, relative := range files {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fixture\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// A fresh clone carries no AGENTS.md: the engine mirrors are generated and
// gitignored. It must still be recognised as the blueprint, or `pfm install`
// run from inside it records no source repository at all.
func TestIsSourceRepoRecognisesAFreshCloneWithoutGeneratedMirrors(t *testing.T) {
	root := t.TempDir()
	writeSourceRepoFixture(t, root, ClaudeInstructionsFile, "pfm/go.mod", ".claude/settings.json")
	if !isSourceRepo(root) {
		t.Fatal("a fresh clone without AGENTS.md was not recognised as the source repository")
	}
}

// A scaffolded adopter project has the instructions file, the settings and a
// compiled AGENTS.md, but never the engine source.
func TestIsSourceRepoRefusesAScaffoldedAdopterProject(t *testing.T) {
	root := t.TempDir()
	writeSourceRepoFixture(t, root, ClaudeInstructionsFile, "AGENTS.md", ".claude/settings.json")
	if isSourceRepo(root) {
		t.Fatal("an adopter project without pfm/go.mod was mistaken for the source repository")
	}
}
