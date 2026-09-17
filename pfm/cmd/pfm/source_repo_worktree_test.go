package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"hostops/pfm/internal/professor"
)

// TestDiscoverSourceRepoMapsALinkedWorktreeToItsMainCheckout pins the source
// clone pfm records when `pfm install` runs from inside a git worktree of it.
// A worktree carries every file isSourceRepo checks, so the walk used to stop
// there and record it — and a worktree is deleted when its wave merges,
// leaving the marker, `pfm update`, and the global links pointing at nothing.
func TestDiscoverSourceRepoMapsALinkedWorktreeToItsMainCheckout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("PFM_SOURCE_REPO", "")
	clone := filepath.Join(t.TempDir(), "clone")
	for _, name := range []string{"CLAUDE.md", "AGENTS.md", ".claude/settings.json", "pfm/go.mod"} {
		path := filepath.Join(clone, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fixture\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git := func(args ...string) {
		t.Helper()
		command := exec.Command(
			"git",
			append(
				[]string{
					"-C",
					clone,
					"-c",
					"user.name=probe",
					"-c",
					"user.email=probe@example.invalid",
					"-c",
					"commit.gpgsign=false",
				},
				args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	git("init", "-q")
	git("add", ".")
	git("commit", "-q", "-m", "probe")
	worktree := filepath.Join(clone, ".worktrees", "wave")
	git("worktree", "add", "--detach", "-q", worktree, "HEAD")

	t.Chdir(filepath.Join(worktree, "pfm"))
	got, err := filepath.EvalSymlinks(professor.DiscoverSourceRepo())
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(clone)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("discoverSourceRepo() from inside a linked worktree = %q, want its main checkout %q", got, want)
	}

	// The main checkout itself still discovers as itself.
	t.Chdir(clone)
	if got, _ := filepath.EvalSymlinks(professor.DiscoverSourceRepo()); got != want {
		t.Fatalf("discoverSourceRepo() from the main checkout = %q, want %q", got, want)
	}
}
