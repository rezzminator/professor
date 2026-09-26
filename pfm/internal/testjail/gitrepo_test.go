package testjail

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGitRepoWithWorktreesBuildsLinkedWorktrees(t *testing.T) {
	repo := GitRepoWithWorktrees(t, "acme")
	for _, dir := range []string{repo.Repo, repo.Other} {
		if info, err := os.Stat(filepath.Join(dir, ".git")); err != nil || !info.IsDir() {
			t.Fatalf("%s/.git = %v, %v; want a directory", dir, info, err)
		}
	}
	for _, dir := range []string{repo.Inner, repo.Outer} {
		if info, err := os.Stat(filepath.Join(dir, ".git")); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("%s/.git = %v, %v; want a linked worktree's file", dir, info, err)
		}
	}
}
