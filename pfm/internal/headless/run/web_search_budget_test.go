package run

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

func TestRunUsesScriptedRunnerStartBoundary(t *testing.T) {
	fake := &deps.FakeRunner{}
	binary := filepath.Join(t.TempDir(), "claude")
	if err := testjail.WriteExecutableNeverRun(binary, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	fake.ScriptStart([]string{binary}, 42, nil, nil)
	result, err := Run(context.Background(), Request{
		Config:         pfmconfig.Config{Claude: pfmconfig.ClaudePrefs{Binary: binary}},
		Engine:         pfmengine.Claude,
		Prompt:         "probe",
		Env:            []string{"CLAUDE_CONFIG_DIR=" + t.TempDir()},
		WithoutAccount: true,
		Native:         true,
		Runner:         fake,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
	calls := fake.Starts()
	if len(calls) != 1 || len(calls[0].Argv) == 0 || calls[0].Argv[0] != binary {
		t.Fatalf("Runner starts = %#v, want one call beginning with %q", calls, binary)
	}
	opts := calls[0].Opts
	wantOpts := deps.StartOptions{
		ProcessGroup: true, WaitDelay: processWaitAfterCancel,
		Env: opts.Env, Dir: opts.Dir, Stdin: opts.Stdin, Stdout: opts.Stdout, Stderr: opts.Stderr,
	}
	if opts.Stdin == nil || opts.Stdout == nil || opts.Stderr == nil || !reflect.DeepEqual(opts, wantOpts) {
		t.Fatalf(
			"Runner options = %#v, want process group, WaitDelay=%s, and all streams",
			opts,
			processWaitAfterCancel,
		)
	}
}

type delayedWaitProcess struct {
	killed  chan struct{}
	release chan struct{}
	waited  chan struct{}
}

func (process delayedWaitProcess) Pid() int { return 42 }

func (process delayedWaitProcess) Wait() error {
	<-process.release
	close(process.waited)
	return errors.New("wait completed after cancellation")
}

func (delayedWaitProcess) Release() error { return nil }

func (delayedWaitProcess) StdinPipe() (io.WriteCloser, error) {
	return nil, errors.New("stdin pipe unavailable")
}

func (delayedWaitProcess) StdoutPipe() (io.ReadCloser, error) {
	return nil, errors.New("stdout pipe unavailable")
}

func (delayedWaitProcess) Kill() error { return nil }

func (process delayedWaitProcess) KillGroup() error {
	close(process.killed)
	return nil
}

func (delayedWaitProcess) SignalGroup(syscall.Signal) error { return nil }

type blockedWaitRunner struct {
	started chan struct{}
	killed  chan struct{}
	release chan struct{}
	waited  chan struct{}
}

func (runner *blockedWaitRunner) Run(context.Context, []string, deps.RunOptions) (deps.RunResult, error) {
	return deps.RunResult{}, nil
}

func (runner *blockedWaitRunner) LookPath(name string) (string, error) { return name, nil }

func (runner *blockedWaitRunner) Start(context.Context, []string, deps.StartOptions) (deps.Process, error) {
	close(runner.started)
	return delayedWaitProcess{killed: runner.killed, release: runner.release, waited: runner.waited}, nil
}

func TestRunProcessWaitsForProcessAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(testwait.Context(t))
	runner := &blockedWaitRunner{
		started: make(chan struct{}), killed: make(chan struct{}),
		release: make(chan struct{}), waited: make(chan struct{}),
	}
	done := make(chan error, 1)
	go func() {
		done <- runProcess(ctx, runner, []string{"engine"}, deps.StartOptions{ProcessGroup: true, WaitDelay: processWaitAfterCancel})
	}()
	testwait.Receive(t, "the runner to start the process", runner.started)
	cancel()
	testwait.Receive(t, "runProcess to kill the process group after the cancellation", runner.killed)
	testwait.Until(t, "runProcess to block on process.Wait after the kill, or to return", func() (bool, string) {
		if len(done) != 0 {
			return true, ""
		}
		return testwait.Blocked("run.runProcess")
	})
	select {
	case err := <-done:
		t.Fatalf("runProcess() returned %v before process.Wait completed", err)
	default:
	}
	close(runner.release)
	err, _ := testwait.Receive(t, "runProcess to return once process.Wait completed", done)
	if got, want := fmt.Sprint(err), "wait completed after cancellation"; got != want {
		t.Fatalf("runProcess() error = %q, want %q", got, want)
	}
	select {
	case <-runner.waited:
	default:
		t.Fatal("runProcess() returned before process.Wait completed")
	}
}

func TestWebSearchBudgetRequiresPassedSettings(t *testing.T) {
	const name = "CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION"
	args, err := arguments(Request{Engine: pfmengine.Claude, Settings: map[string]any{"webSearchesPerSession": 7}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(
		args,
		" ",
	), "-p --output-format json --settings {\"cleanupPeriodDays\":36500,\"env\":{\"ANTHROPIC_SMALL_FAST_MODEL\":\"claude-haiku-5-5\",\"CLAUDE_CODE_AUTO_COMPACT_WINDOW\":\"100000\",\"CLAUDE_CODE_DISABLE_BG_EXIT_HANDOFF\":\"1\",\"CLAUDE_CODE_DISABLE_FEEDBACK_SURVEY\":\"1\",\"CLAUDE_CODE_ENABLE_FUNCTION_HOOKS\":\"1\",\"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS\":\"0\",\"CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH\":\"8\",\"CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION\":\"7\",\"CLAUDE_CODE_NATIVE_CURSOR\":\"1\",\"CLAUDE_CODE_RETRY_WATCHDOG\":\"1\",\"CLAUDE_CODE_STOP_HOOK_BLOCK_CAP\":\"2\",\"CLAUDE_CODE_TMUX_TRUECOLOR\":\"1\",\"CLAUDE_DISABLE_ADOPT\":\"1\",\"ENABLE_TOOL_SEARCH\":\"true\"},\"outputStyle\":\"default\",\"showThinkingSummaries\":true,\"skipAutoPermissionPrompt\":true,\"skipDangerousModePermissionPrompt\":true,\"skipWorkflowUsageWarning\":true,\"tui\":\"fullscreen\"}"; got != want {
		t.Fatalf("args = %q, want %q", got, want)
	}
	var environment []string
	setEnvironment([]string{"PATH=/usr/bin"}, pfmengine.Claude, "/cfg", false, &environment)
	if got := lastEnvironmentValue(environment, name); got != "" {
		t.Fatalf("web-search budget came from descriptor environment: %q", environment)
	}
}

func lastEnvironmentValue(environment []string, name string) string {
	value := ""
	for _, entry := range environment {
		if key, rest, found := strings.Cut(entry, "="); found && key == name {
			value = rest
		}
	}
	return value
}
