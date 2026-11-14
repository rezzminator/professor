package fakegit

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// TestFakeGitSyncsWithARemoteAndLinkedWorkTrees: push carries tags to a bare
// remote ls-remote reads, fetch brings a tag back to a copy that lacks it and
// leaves the remote's own remote-tracking refs behind, a remote naming no
// repository's root refuses, a short push destination is outside the model,
// and a linked work tree is checked out, answers from its own HEAD and goes
// away, a second one of the same base name getting its own admin directory.
func TestFakeGitSyncsWithARemoteAndLinkedWorkTrees(t *testing.T) {
	git, repo := newFakeGitRepo(t)
	first := commitFakeGitFiles(t, git, repo, "A", map[string]string{"a": "a\n"})
	git.Must(t, repo, "tag", "v1")
	remote := filepath.Join(t.TempDir(), "remote.git")
	git.Must(t, remote, "init", "--bare", "-q", remote)
	git.Must(t, repo, "remote", "add", "origin", remote)
	git.Must(t, repo, "push", "-q", "origin", "HEAD", "--tags")
	if got, want := git.Must(
		t,
		repo,
		"ls-remote",
		"--tags",
		"origin",
		"refs/tags/v1",
	), first+"\trefs/tags/v1"; got != want {
		t.Fatalf("ls-remote = %q, want %q", got, want)
	}
	second := commitFakeGitFiles(t, git, repo, "B", map[string]string{"a": "b\n"})
	git.Must(t, repo, "tag", "v2")
	git.Must(t, repo, "push", "-q", "origin", "v2")
	_, clone := newFakeGitRepo(t)
	git.Must(t, clone, "remote", "add", "origin", remote)
	git.Must(t, clone, "fetch", "--tags")
	if tags, v2 := git.Must(
		t,
		clone,
		"tag",
		"--list",
	), git.Must(
		t,
		clone,
		"rev-parse",
		"--verify",
		"v2",
	); tags != "v1\nv2" ||
		v2 != second {
		t.Fatalf("after fetch --tags: tags %q, v2 %s; want v1 and v2, v2 %s", tags, v2, second)
	}
	stage := filepath.Join(t.TempDir(), "stage")
	git.Must(t, repo, "worktree", "add", "--detach", "--quiet", stage, "v1")
	if head, tree := git.Must(t, stage, "rev-parse", "HEAD"), fakeGitTree(t, stage); head != first || tree != "a=a\n" {
		t.Fatalf("linked work tree: HEAD %s, tree %q; want %s, a=a", head, tree, first)
	}
	if got := git.Must(t, repo, "rev-parse", "HEAD"); got != second {
		t.Fatalf("the main work tree moved with the linked one: HEAD %s, want %s", got, second)
	}
	admin := filepath.Join(repo, fakeGitDirName, "worktrees", "stage")
	for name, want := range map[string]string{"commondir": "../..\n", "gitdir": filepath.Join(stage, fakeGitDirName) + "\n"} {
		if got, err := os.ReadFile(filepath.Join(admin, name)); err != nil || string(got) != want {
			t.Fatalf("worktree admin file %s = %q (%v), want %q", name, got, err, want)
		}
	}
	git.Must(t, repo, "worktree", "remove", "--force", stage)
	if _, err := os.Stat(admin); !os.IsNotExist(err) {
		t.Fatalf("worktree remove left its admin dir %s: %v", admin, err)
	}
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatalf("worktree remove left %s: %v", stage, err)
	}
	first1, second1 := filepath.Join(t.TempDir(), "stage"), filepath.Join(t.TempDir(), "stage")
	git.Must(t, repo, "worktree", "add", "--detach", "--quiet", first1, "v1")
	git.Must(t, repo, "worktree", "add", "--detach", "--quiet", second1, "v2")
	if gitDir, a, b := git.Must(t, second1, "rev-parse", "--git-dir"), git.Must(t, first1, "rev-parse", "HEAD"),
		git.Must(t, second1, "rev-parse", "HEAD"); gitDir != admin+"1" || a != first || b != second {
		t.Errorf("two linked work trees named stage: git dir %s, HEADs %s %s; want %s1, %s %s",
			gitDir, a, b, admin, first, second)
	}
	git.Must(t, repo, "worktree", "remove", "--force", second1)
	if got := git.Must(t, first1, "rev-parse", "HEAD"); got != first {
		t.Errorf("removing the second stage moved the first: HEAD %s, want %s", got, first)
	}

	_, third := newFakeGitRepo(t)
	git.Must(t, third, "remote", "add", "clone", clone)
	git.Must(t, third, "fetch", "--tags", "clone")
	if mirrored, tag := fakeGitRun(t, git, third, []string{}, "rev-parse", "--verify", "origin/master"),
		git.Must(t, third, "rev-parse", "--verify", "v2"); mirrored.ExitCode != 128 || tag != second {
		t.Errorf("fetch --tags from clone: origin/master exit %d (want 128), v2 %s (want %s)",
			mirrored.ExitCode, tag, second)
	}
	if err := os.MkdirAll(filepath.Join(repo, "plain"), 0o755); err != nil {
		t.Fatal(err)
	}
	git.Must(t, third, "remote", "add", "plain", filepath.Join(repo, "plain"))
	if result := fakeGitRun(t, git, third, []string{}, "fetch", "plain"); result.ExitCode != 128 {
		t.Errorf("fetch from a directory inside a repository: exit %d, want 128", result.ExitCode)
	}
	if _, err := git.Run(
		context.Background(), []string{"git", "push", "origin", "HEAD:main"}, deps.RunOptions{Dir: repo},
	); err == nil {
		t.Errorf("push origin HEAD:main answered; want an error naming the argv")
	}
}
