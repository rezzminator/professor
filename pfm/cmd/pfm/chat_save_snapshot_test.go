package main

import (
	"bytes"
	"context"
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

func TestWriteRepositorySnapshotReportsWhyNoSnapshotWasTaken(t *testing.T) {
	for _, tc := range []struct {
		name       string
		gitMissing bool
		want       string
		wantCalls  int
	}{
		{name: "outside a repository", want: "(not a git repository)\n", wantCalls: 1},
		{
			name:       "git missing from PATH",
			gitMissing: true,
			want:       "(repository snapshot failed: resolve git: exec: \"git\": executable file not found in $PATH)\n",
			wantCalls:  0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &deps.FakeRunner{}
			runner.Script(
				[]string{deps.Executable("git"), "rev-parse", "--is-inside-work-tree"},
				deps.RunResult{ExitCode: 128},
				nil,
			)
			if tc.gitMissing {
				t.Setenv("PATH", t.TempDir())
			}
			var buf bytes.Buffer
			writeRepositorySnapshot(context.Background(), &buf, runner)
			if got := buf.String(); got != tc.want {
				t.Fatalf("writeRepositorySnapshot() = %q, want %q", got, tc.want)
			}
			if got := len(runner.Calls()); got != tc.wantCalls {
				t.Fatalf("git calls = %d, want %d", got, tc.wantCalls)
			}
		})
	}
}

func TestWriteRepositorySnapshotRendersEachPartItCouldRead(t *testing.T) {
	git := deps.Executable("git")
	tests := []struct {
		name   string
		status deps.RunResult
		want   string
	}{
		{
			name:   "every part read",
			status: deps.RunResult{Stdout: []byte(" M cmd/pfm/x.go\n")},
			want: "Branch: wave/testing\n\n```\n M cmd/pfm/x.go\n```\n\n" +
				"Worktrees:\n```\n/repo  abc123 [main]\n```\n",
		},
		{
			name:   "status failed",
			status: deps.RunResult{ExitCode: 128},
			want: "Branch: wave/testing\n\n```\n(failed: exit status 128)\n```\n\n" +
				"Worktrees:\n```\n/repo  abc123 [main]\n```\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runner := &deps.FakeRunner{}
			runner.Script([]string{git, "rev-parse", "--is-inside-work-tree"}, deps.RunResult{ExitCode: 0}, nil)
			branch := deps.RunResult{Stdout: []byte("wave/testing\n")}
			runner.Script([]string{git, branchAction, "--show-current"}, branch, nil)
			runner.Script([]string{git, "status", "--short"}, tc.status, nil)
			worktrees := deps.RunResult{Stdout: []byte("/repo  abc123 [main]\n")}
			runner.Script([]string{git, "worktree", "list"}, worktrees, nil)
			var buf bytes.Buffer
			writeRepositorySnapshot(context.Background(), &buf, runner)
			if got := buf.String(); got != tc.want {
				t.Fatalf("writeRepositorySnapshot() = %q, want %q", got, tc.want)
			}
		})
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
