package deps

import (
	"context"
	"errors"
	"os/exec"
	"testing"
)

func TestRealRunnerRunCapturesStdoutStderrAndExitCode(t *testing.T) {
	var runner RealRunner
	result, err := runner.Run(
		context.Background(),
		[]string{"sh", "-c", "echo out; echo err >&2; exit 3"},
		RunOptions{},
	)
	if err != nil {
		t.Fatalf("Run() error = %v, want nil (a nonzero exit is not a Runner failure)", err)
	}
	if got := string(result.Stdout); got != "out\n" {
		t.Fatalf("Stdout = %q, want %q", got, "out\n")
	}
	if got := string(result.Stderr); got != "err\n" {
		t.Fatalf("Stderr = %q, want %q", got, "err\n")
	}
	if result.ExitCode != 3 {
		t.Fatalf("ExitCode = %d, want 3", result.ExitCode)
	}
}

func TestRealRunnerRunReturnsAnErrorWhenTheBinaryCannotStart(t *testing.T) {
	var runner RealRunner
	_, err := runner.Run(context.Background(), []string{"pfm-runner-test-no-such-binary"}, RunOptions{})
	if err == nil {
		t.Fatal("Run() with a nonexistent binary returned nil error")
	}
}

func TestRealRunnerRunRespectsEnvAndStdin(t *testing.T) {
	var runner RealRunner
	result, err := runner.Run(context.Background(), []string{"sh", "-c", "echo \"$GREETING $(cat)\""}, RunOptions{
		Env:   []string{"GREETING=hi"},
		Stdin: []byte("there"),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := string(result.Stdout); got != "hi there\n" {
		t.Fatalf("Stdout = %q, want %q", got, "hi there\n")
	}
}

func TestRealRunnerLookPathResolvesAKnownBinary(t *testing.T) {
	var runner RealRunner
	path, err := runner.LookPath("sh")
	if err != nil {
		t.Fatalf("LookPath(sh) error = %v", err)
	}
	if path == "" {
		t.Fatal("LookPath(sh) returned an empty path with a nil error")
	}
}

func TestFakeRunnerScriptsByLongestPrefixMatch(t *testing.T) {
	fake := &FakeRunner{}
	fake.Script([]string{"git"}, RunResult{Stdout: []byte("broad")}, nil)
	fake.Script([]string{"git", "push"}, RunResult{Stdout: []byte("narrow")}, nil)

	broad, err := fake.Run(context.Background(), []string{"git", "status"}, RunOptions{})
	if err != nil {
		t.Fatalf("Run(git status) error = %v", err)
	}
	if got := string(broad.Stdout); got != "broad" {
		t.Fatalf("Run(git status) Stdout = %q, want %q", got, "broad")
	}

	narrow, err := fake.Run(context.Background(), []string{"git", "push", "origin"}, RunOptions{})
	if err != nil {
		t.Fatalf("Run(git push origin) error = %v", err)
	}
	if got := string(narrow.Stdout); got != "narrow" {
		t.Fatalf("Run(git push origin) Stdout = %q, want %q (longest prefix must win)", got, "narrow")
	}
}

func TestFakeRunnerUnscriptedCallFailsLoudWithTheArgv(t *testing.T) {
	fake := &FakeRunner{}
	_, err := fake.Run(context.Background(), []string{"codex", "doctor"}, RunOptions{})
	if err == nil {
		t.Fatal("Run() on an unscripted argv returned nil error")
	}
	var unscripted UnscriptedError
	if !errors.As(err, &unscripted) {
		t.Fatalf("Run() error = %v (%T), want an UnscriptedError", err, err)
	}
	if len(unscripted.Argv) != 2 || unscripted.Argv[0] != "codex" || unscripted.Argv[1] != "doctor" {
		t.Fatalf("UnscriptedError.Argv = %v, want [codex doctor]", unscripted.Argv)
	}
}

func TestFakeRunnerLookPathScriptedAndUnscripted(t *testing.T) {
	fake := &FakeRunner{}
	fake.ScriptLookPath("git", "/usr/bin/git", nil)

	path, err := fake.LookPath("git")
	if err != nil || path != "/usr/bin/git" {
		t.Fatalf("LookPath(git) = (%q, %v), want (/usr/bin/git, nil)", path, err)
	}

	_, err = fake.LookPath("codex")
	if err == nil {
		t.Fatal("LookPath(codex) with nothing scripted returned nil error")
	}
	if !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("LookPath(codex) error = %v, want exec.ErrNotFound (ENOENT)", err)
	}
}

func TestFakeRunnerCallsRecordsEveryRunInOrder(t *testing.T) {
	fake := &FakeRunner{}
	fake.Script([]string{"git"}, RunResult{}, nil)
	if _, err := fake.Run(context.Background(), []string{"git", "status"}, RunOptions{Dir: "/repo"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if _, err := fake.Run(context.Background(), []string{"git", "log"}, RunOptions{}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	calls := fake.Calls()
	if len(calls) != 2 {
		t.Fatalf("Calls() returned %d entries, want 2", len(calls))
	}
	if calls[0].Argv[1] != "status" || calls[0].Opts.Dir != "/repo" {
		t.Fatalf("Calls()[0] = %+v, want argv[1]=status, Dir=/repo", calls[0])
	}
	if calls[1].Argv[1] != "log" {
		t.Fatalf("Calls()[1] = %+v, want argv[1]=log", calls[1])
	}

	// Calls() must return a copy: mutating it never reaches the ledger.
	calls[0].Argv[0] = "mutated"
	if fresh := fake.Calls(); fresh[0].Argv[0] != "git" {
		t.Fatalf("Calls()[0].Argv[0] = %q after external mutation, want unaffected %q", fresh[0].Argv[0], "git")
	}
}
