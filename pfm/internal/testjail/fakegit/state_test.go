package fakegit

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFakeGitFindsTheRepositoryItRunsIn: the nearest repository at or above the
// directory, the one GIT_DIR names over it, and none outside any.
func TestFakeGitFindsTheRepositoryItRunsIn(t *testing.T) {
	git, repo := newFakeGitRepo(t)
	head := commitFakeGitFiles(t, git, repo, "A", map[string]string{"src/main.go": "package main\n"})
	_, other := newFakeGitRepo(t)
	otherHead := commitFakeGitFiles(t, git, other, "other", map[string]string{"other.txt": "other\n"})
	outside := t.TempDir()
	for _, tc := range []struct {
		name, dir string
		env       []string
		code      int
		stdout    string
	}{
		{"subdirectory", filepath.Join(repo, "src"), []string{}, 0, head + "\n"},
		{"GIT_DIR", repo, []string{"GIT_DIR=" + filepath.Join(other, ".git")}, 0, otherHead + "\n"},
		{"outside any repository", outside, []string{}, 128, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := fakeGitRun(t, git, tc.dir, tc.env, "rev-parse", "--verify", "HEAD")
			if result.ExitCode != tc.code || string(result.Stdout) != tc.stdout {
				t.Fatalf("rev-parse in %s: exit %d stdout %q, want exit %d stdout %q",
					tc.dir, result.ExitCode, result.Stdout, tc.code, tc.stdout)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(repo, ".git", fakeGitStateFile)); err != nil {
		t.Fatalf("the model keeps no state beside the fixture: %v", err)
	}
}
