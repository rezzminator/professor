package run

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	pfmconfig "hostops/pfm/internal/config"
	"hostops/pfm/internal/deps"
	pfmengine "hostops/pfm/internal/engine"
)

func TestRunUsesScriptedRunnerStartBoundary(t *testing.T) {
	fake := &deps.FakeRunner{}
	binary := deps.Executable("sh")
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
		if err == nil || !strings.Contains(err.Error(), "wait completed") {
			t.Fatalf("runProcess() error = %v, want the completed Wait result", err)
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

// A headless Claude run is a door of its own (it never renders through
// action.ClaudeSpawn), so it must lift Claude Code's 200-call WebSearch cap
// too — for inherited and explicit environments alike — while a Codex run
// carries nothing of Claude's.
func TestSetEnvironmentCarriesClaudeWebSearchBudget(t *testing.T) {
	const name, value = "CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION", "9007199254740991"
	for _, explicit := range []bool{false, true} {
		var claude []string
		setEnvironment([]string{name + "=5", "PATH=/usr/bin"}, pfmengine.Claude, "/cfg", explicit, &claude)
		if got := lastEnvironmentValue(claude, name); got != value {
			t.Fatalf(
				"claude headless environment (explicit=%t) %q resolves %s=%q, want %q",
				explicit,
				claude,
				name,
				got,
				value,
			)
		}
		var codex []string
		setEnvironment([]string{"PATH=/usr/bin"}, pfmengine.Codex, "/cfg", explicit, &codex)
		if got := lastEnvironmentValue(codex, name); got != "" {
			t.Fatalf("codex headless environment (explicit=%t) %q carries Claude's %s=%q", explicit, codex, name, got)
		}
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
