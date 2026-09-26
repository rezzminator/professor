package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/headless"
)

type snapshotContextKey struct{}

type snapshotContextRunner struct {
	*deps.FakeRunner
	contexts []context.Context
}

func (runner *snapshotContextRunner) Run(
	ctx context.Context,
	argv []string,
	opts deps.RunOptions,
) (deps.RunResult, error) {
	runner.contexts = append(runner.contexts, ctx)
	return runner.FakeRunner.Run(ctx, argv, opts)
}

func TestWriteRepositorySnapshotReportsOutsideAGitRepository(t *testing.T) {
	git := deps.Executable("git")
	runner := &deps.FakeRunner{}
	runner.Script([]string{git, "rev-parse", "--is-inside-work-tree"}, deps.RunResult{ExitCode: 128}, nil)
	var buf bytes.Buffer
	writeRepositorySnapshot(context.Background(), &buf, runner)
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
	writeRepositorySnapshot(context.Background(), &buf, runner)
	got := buf.String()
	if !strings.Contains(got, "Branch: wave/testing") ||
		!strings.Contains(got, " M cmd/pfm/x.go") ||
		!strings.Contains(got, "/repo  abc123 [main]") {
		t.Fatalf("writeRepositorySnapshot() = %q, want branch/status/worktrees rendered", got)
	}
}

func TestWriteRepositorySnapshotContextPassesCallerContextAndDirectoryToEveryGitProbe(t *testing.T) {
	git := deps.Executable("git")
	runner := &snapshotContextRunner{FakeRunner: &deps.FakeRunner{}}
	runner.Script([]string{git, "rev-parse", "--is-inside-work-tree"}, deps.RunResult{ExitCode: 0}, nil)
	runner.Script([]string{git, branchAction, "--show-current"}, deps.RunResult{ExitCode: 0}, nil)
	runner.Script([]string{git, "status", "--short"}, deps.RunResult{ExitCode: 0}, nil)
	runner.Script([]string{git, "worktree", "list"}, deps.RunResult{ExitCode: 0}, nil)
	const callerDir = "/caller/repository"
	ctx := context.WithValue(context.Background(), snapshotContextKey{}, "caller")
	ctx = pfmchat.WithResolvedSelf(ctx, headless.Chat{CWD: callerDir})
	var buf bytes.Buffer

	writeRepositorySnapshot(ctx, &buf, runner)

	calls := runner.Calls()
	if len(calls) != 4 || len(runner.contexts) != 4 {
		t.Fatalf("git calls=%d contexts=%d, want four of each", len(calls), len(runner.contexts))
	}
	for index := range calls {
		if calls[index].Opts.Dir != callerDir {
			t.Errorf("git call %d directory = %q, want %q", index, calls[index].Opts.Dir, callerDir)
		}
		if got := runner.contexts[index].Value(snapshotContextKey{}); got != "caller" {
			t.Errorf("git call %d context marker = %v, want caller", index, got)
		}
	}
}

func TestWriteRepositorySnapshotKeepsAmbientDirectoryEmpty(t *testing.T) {
	git := deps.Executable("git")
	runner := &deps.FakeRunner{}
	runner.Script([]string{git, "rev-parse", "--is-inside-work-tree"}, deps.RunResult{ExitCode: 128}, nil)
	var buf bytes.Buffer

	writeRepositorySnapshot(context.Background(), &buf, runner)

	calls := runner.Calls()
	if len(calls) != 1 || calls[0].Opts.Dir != "" {
		t.Fatalf("ambient snapshot calls = %+v, want one probe with an empty directory", calls)
	}
}
