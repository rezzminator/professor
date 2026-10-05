package gitroot

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestRepoRootMapsEveryPlaceInARepositoryToItsMainCheckout(t *testing.T) {
	fixture := testjail.GitRepoWithWorktrees(t, "acme")
	base, repo, subdir, inner, outer := fixture.Base, fixture.Repo, fixture.Subdir, fixture.Inner, fixture.Outer
	plain := filepath.Join(base, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name, dir, want string
	}{
		{"non-git dir", plain, plain},
		{"repo root", repo, repo},
		{"repo subdirectory", subdir, repo},
		{"worktree under .worktrees", filepath.Join(inner, "src"), repo},
		{"second repository", fixture.Other, fixture.Other},
		{"worktree outside the repo", outer, repo},
		{"deleted worktree", filepath.Join(repo, ".worktrees", "gone", "pfm"), repo},
		{"deleted worktree root", filepath.Join(repo, ".worktrees", "gone"), repo},
		{"deleted plain dir", filepath.Join(base, "vanished"), filepath.Join(base, "vanished")},
		{"relative dir", "acme/src", "acme/src"},
	} {
		if got := RepoRoot(testCase.dir); got != testCase.want {
			t.Errorf("%s: RepoRoot(%q) = %q, want %q", testCase.name, testCase.dir, got, testCase.want)
		}
	}
	if main, ok := MainCheckout(repo); ok {
		t.Errorf("MainCheckout(main checkout) = %q, true; want not a linked worktree", main)
	}
}

// A .git file whose gitdir cannot be followed falls back to the directory
// holding it, never to an error or a walk past it.
func TestRepoRootFallsBackOnAnUnreadableGitFile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "broken")
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: /nonexistent/gitdir\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := RepoRoot(filepath.Join(root, "sub")); got != root {
		t.Fatalf("RepoRoot under a dangling .git file = %q, want %q", got, root)
	}
}

func TestProjectLabelsADirectoryByItsRepository(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "acme")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	subdir := filepath.Join(repo, "src")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(base, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture := testjail.GitRepoWithWorktrees(t, "acme")

	for _, testCase := range []struct {
		name, dir, wantRoot, wantName string
	}{
		{"repository subdirectory", subdir, repo, "acme"},
		{"plain directory", plain, plain, "plain"},
		{"empty directory", "", "", "?"},
		{"filesystem root", string(filepath.Separator), string(filepath.Separator), "?"},
		{"linked worktree", fixture.Inner, fixture.Repo, "acme"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			root, name := Project(testCase.dir)
			if root != testCase.wantRoot || name != testCase.wantName {
				t.Fatalf("Project(%q) = (%q, %q), want (%q, %q)",
					testCase.dir, root, name, testCase.wantRoot, testCase.wantName)
			}
		})
	}
}
