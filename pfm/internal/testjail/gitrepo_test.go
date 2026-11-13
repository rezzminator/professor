package testjail_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/testjail/fakegit"
)

func TestGitRepoWithWorktreesBuildsLinkedWorktrees(t *testing.T) {
	repo := testjail.GitRepoWithWorktrees(t, &fakegit.Runner{}, "acme")
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

// fatalRecorder is a testing.TB whose Fatalf records its message and ends the goroutine, as the
// real one does, without failing the test that holds it.
type fatalRecorder struct {
	testing.TB
	message string
}

func (r *fatalRecorder) Helper() {}

func (r *fatalRecorder) Fatalf(format string, args ...any) {
	r.message = strings.TrimSpace(fmt.Sprintf(format, args...))
	runtime.Goexit()
}

// TestGitRepoWithWorktreesFailsWhenItsRunnerFindsNoGit: a runner without git fails the test with
// the lookup's own error, never a skip.
func TestGitRepoWithWorktreesFailsWhenItsRunnerFindsNoGit(t *testing.T) {
	recorder := &fatalRecorder{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		testjail.GitRepoWithWorktrees(recorder, &deps.FakeRunner{}, "acme")
	}()
	<-done
	want := `git fixture: look up git: exec: "git": executable file not found in $PATH`
	if recorder.message != want {
		t.Fatalf("GitRepoWithWorktrees with no git failed with %q, want %q", recorder.message, want)
	}
}
