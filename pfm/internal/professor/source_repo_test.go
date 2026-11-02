package professor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/testjail"
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
	if !IsSourceRepo(root) {
		t.Fatal("a fresh clone without AGENTS.md was not recognised as the source repository")
	}
}

// A scaffolded adopter project has the instructions file, the settings and a
// compiled AGENTS.md, but never the engine source.
func TestIsSourceRepoRefusesAScaffoldedAdopterProject(t *testing.T) {
	root := t.TempDir()
	writeSourceRepoFixture(t, root, ClaudeInstructionsFile, "AGENTS.md", ".claude/settings.json")
	if IsSourceRepo(root) {
		t.Fatal("an adopter project without pfm/go.mod was mistaken for the source repository")
	}
}

// A working directory that cannot be resolved is a source clone pfm could not
// look for, never a quiet "no clone": the lookup still answers "" and logs
// the cause at warn.
func TestDiscoverSourceRepoLogsAnUnresolvableWorkingDirectory(t *testing.T) {
	_, recorder := obs.Test(t)
	t.Setenv("PFM_SOURCE_REPO", "")
	testjail.ChdirUnnamable(t)
	if got := DiscoverSourceRepo(); got != "" {
		t.Fatalf("DiscoverSourceRepo() = %q, want \"\" from an unnamable working directory", got)
	}
	records := recorder.Records()
	if len(records) != 1 || records[0].Message != "professor.source_repo.unreadable" || records[0].Level != "WARN" {
		t.Fatalf("records = %+v, want one WARN professor.source_repo.unreadable", records)
	}
	if got, _ := records[0].Field(obs.FieldErr); got != "getwd: file name too long" {
		t.Fatalf("err field = %v, want getwd: file name too long", got)
	}
}
