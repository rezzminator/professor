package testjail

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// GitRepo is a real repository built by GitRepoWithWorktrees under one temp
// base: Repo (named by the caller) with a committed Subdir, a linked worktree
// Inner under Repo/.worktrees/, one Outer outside the repo tree, and Other, a
// second repository of its own.
type GitRepo struct {
	Base, Repo, Subdir, Inner, Outer, Other string
}

// GitRepoWithWorktrees builds a GitRepo with the git binary, skipping the test
// when git is not installed.
func GitRepoWithWorktrees(t *testing.T, name string) GitRepo {
	t.Helper()
	runner := deps.RealRunner{}
	gitBinary, err := runner.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	base := t.TempDir()
	repo := GitRepo{
		Base:   base,
		Repo:   filepath.Join(base, name),
		Subdir: filepath.Join(base, name, "src"),
		Inner:  filepath.Join(base, name, ".worktrees", "test-junk"),
		Outer:  filepath.Join(base, "elsewhere", "feature-x"),
		Other:  filepath.Join(base, "other-repo"),
	}
	for _, dir := range []string{repo.Subdir, repo.Other} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("git fixture: create %s: %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
			t.Fatalf("git fixture: seed %s: %v", dir, err)
		}
	}
	git := func(dir string, args ...string) {
		t.Helper()
		argv := append([]string{
			gitBinary, "-C", dir, "-c", "user.name=probe", "-c", "user.email=probe@example.invalid",
			"-c", "commit.gpgsign=false",
		}, args...)
		result, err := runner.Run(context.Background(), argv, deps.RunOptions{})
		if err != nil || result.ExitCode != 0 {
			t.Fatalf("git fixture: git -C %s %v: exit %d: %v: %s", dir, args, result.ExitCode, err, result.Stderr)
		}
	}
	for _, dir := range []string{repo.Repo, repo.Other} {
		git(dir, "init", "-q")
		git(dir, "add", ".")
		git(dir, "commit", "-q", "-m", "probe")
	}
	git(repo.Repo, "worktree", "add", "--detach", "-q", repo.Inner, "HEAD")
	git(repo.Repo, "worktree", "add", "--detach", "-q", repo.Outer, "HEAD")
	return repo
}
