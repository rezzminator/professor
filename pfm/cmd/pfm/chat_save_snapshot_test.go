package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

func TestWriteRepositorySnapshotReportsOutsideAGitRepository(t *testing.T) {
	git := deps.Executable("git")
	runner := &deps.FakeRunner{}
	runner.Script([]string{git, "rev-parse", "--is-inside-work-tree"}, deps.RunResult{ExitCode: 128}, nil)
	var buf bytes.Buffer
	writeRepositorySnapshot(&buf, runner)
	if got := buf.String(); got != "(not a git repository)\n" {
		t.Fatalf("writeRepositorySnapshot() outside a repo = %q", got)
	}
}

func TestWriteRepositorySnapshotRendersBranchStatusAndWorktrees(t *testing.T) {
	git := deps.Executable("git")
	runner := &deps.FakeRunner{}
	runner.Script([]string{git, "rev-parse", "--is-inside-work-tree"}, deps.RunResult{ExitCode: 0}, nil)
	runner.Script([]string{git, branchAction, "--show-current"}, deps.RunResult{Stdout: []byte("wave/testing\n")}, nil)
	runner.Script([]string{git, "status", "--short"}, deps.RunResult{Stdout: []byte(" M cmd/pfm/x.go\n")}, nil)
	runner.Script([]string{git, "worktree", "list"}, deps.RunResult{Stdout: []byte("/repo  abc123 [main]\n")}, nil)
	var buf bytes.Buffer
	writeRepositorySnapshot(&buf, runner)
	got := buf.String()
	if !strings.Contains(got, "Branch: wave/testing") ||
		!strings.Contains(got, " M cmd/pfm/x.go") ||
		!strings.Contains(got, "/repo  abc123 [main]") {
		t.Fatalf("writeRepositorySnapshot() = %q, want branch/status/worktrees rendered", got)
	}
}
