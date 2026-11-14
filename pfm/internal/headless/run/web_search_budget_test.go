package run

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/testjail"
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
	if !calls[0].Opts.ProcessGroup || calls[0].Opts.WaitDelay != processWaitAfterCancel || calls[0].Opts.Stdin == nil ||
		calls[0].Opts.Stdout == nil ||
		calls[0].Opts.Stderr == nil {
		t.Fatalf(
			"Runner options = %#v, want process group, WaitDelay=%s, and all streams",
			calls[0].Opts,
			processWaitAfterCancel,
		)
	}
}

type delayedWaitProcess struct {
	waited chan struct{}
}

func (process delayedWaitProcess) Pid() int { return 42 }

func (process delayedWaitProcess) Wait() error {
	time.Sleep(20 * time.Millisecond)
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

func (delayedWaitProcess) KillGroup() error { return nil }

func (delayedWaitProcess) SignalGroup(syscall.Signal) error { return nil }

type blockedWaitRunner struct {
	started chan struct{}
	waited  chan struct{}
}

func (runner *blockedWaitRunner) Run(context.Context, []string, deps.RunOptions) (deps.RunResult, error) {
	return deps.RunResult{}, nil
}

func (runner *blockedWaitRunner) LookPath(name string) (string, error) { return name, nil }

func (runner *blockedWaitRunner) Start(context.Context, []string, deps.StartOptions) (deps.Process, error) {
	close(runner.started)
	return delayedWaitProcess{waited: runner.waited}, nil
}

func TestRunProcessWaitsForProcessAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runner := &blockedWaitRunner{started: make(chan struct{}), waited: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		done <- runProcess(ctx, runner, []string{"engine"}, deps.StartOptions{ProcessGroup: true, WaitDelay: processWaitAfterCancel})
	}()
	<-runner.started
	cancel()

	select {
	case err := <-done:
		if got, want := fmt.Sprint(err), "wait completed after cancellation"; got != want {
			t.Fatalf("runProcess() error = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("runProcess() did not wait for process.Wait after cancellation")
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
	), "-p --output-format json --settings {\"env\":{\"ANTHROPIC_SMALL_FAST_MODEL\":\"claude-haiku-5-5\",\"CLAUDE_CODE_AUTO_COMPACT_WINDOW\":\"100000\",\"CLAUDE_CODE_ENABLE_FUNCTION_HOOKS\":\"1\",\"CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION\":\"7\"},\"outputStyle\":\"default\"}"; got != want {
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
