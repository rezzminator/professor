package deps

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
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

func TestRealRunnerStartCapturesStdoutStderrAndWaits(t *testing.T) {
	t.Parallel()
	var runner RealRunner
	var stdout, stderr bytes.Buffer
	process, err := runner.Start(
		context.Background(),
		[]string{"sh", "-c", "echo out; echo err >&2; exit 0"},
		StartOptions{Stdout: &stdout, Stderr: &stderr},
	)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if process.Pid() <= 0 {
		t.Fatalf("Pid() = %d, want > 0", process.Pid())
	}
	if err := process.Wait(); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if got := stdout.String(); got != "out\n" {
		t.Fatalf("Stdout = %q, want %q", got, "out\n")
	}
	if got := stderr.String(); got != "err\n" {
		t.Fatalf("Stderr = %q, want %q", got, "err\n")
	}
}

func TestRealRunnerStartWiresOrdinaryStdin(t *testing.T) {
	t.Parallel()
	input := strings.NewReader("hello\n")
	var stdout bytes.Buffer
	process, err := (RealRunner{}).Start(
		context.Background(),
		[]string{"sh", "-c", "read line; printf 'reply:%s\\n' \"$line\""},
		StartOptions{Stdin: input, Stdout: &stdout},
	)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := process.Wait(); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if got := stdout.String(); got != "reply:hello\n" {
		t.Fatalf("Stdout = %q, want %q", got, "reply:hello\\n")
	}
}

func TestRealRunnerStartDetachReleasesAndWaitNamesIt(t *testing.T) {
	t.Parallel()
	var runner RealRunner
	process, err := runner.Start(
		context.Background(),
		[]string{"sh", "-c", "exit 0"},
		StartOptions{Detach: true},
	)
	if err != nil {
		t.Fatalf("Start(Detach) error = %v", err)
	}
	if process.Pid() <= 0 {
		t.Fatalf("Pid() = %d, want > 0", process.Pid())
	}
	if err := process.Wait(); err == nil {
		t.Fatal("Wait() on a detached, released process returned nil error, want one naming the release")
	}
	if err := process.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
}

func TestRealRunnerStartReturnsAnErrorWhenTheBinaryCannotStart(t *testing.T) {
	t.Parallel()
	var runner RealRunner
	_, err := runner.Start(context.Background(), []string{"pfm-runner-test-no-such-binary"}, StartOptions{})
	if err == nil {
		t.Fatal("Start() with a nonexistent binary returned nil error")
	}
}

func TestRealRunnerStartOwnsInteractivePipesAndProcessGroup(t *testing.T) {
	t.Parallel()
	process, err := (RealRunner{}).Start(
		context.Background(),
		[]string{"sh", "-c", "read line; printf 'reply:%s\\n' \"$line\""},
		StartOptions{
			StdinPipe:    true,
			StdoutPipe:   true,
			ProcessGroup: true,
		},
	)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	stdin, err := process.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe() error = %v", err)
	}
	stdout, err := process.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe() error = %v", err)
	}
	if _, err := io.WriteString(stdin, "hello\n"); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	if err := stdin.Close(); err != nil {
		t.Fatalf("close stdin: %v", err)
	}
	body, err := io.ReadAll(stdout)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if got := string(body); got != "reply:hello\n" {
		t.Fatalf("stdout = %q, want reply\n", got)
	}
	if err := process.Wait(); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
}

// TestRealRunnerStartWaitDelayClosesInheritedPipe proves the process seam
// retains exec.Cmd's inherited-descriptor protection. The shell exits, while
// its background child keeps stdout open; Wait must still return at the
// configured delay instead of waiting for that child.
func TestRealRunnerStartWaitDelayClosesInheritedPipe(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	process, err := (RealRunner{}).Start(
		context.Background(),
		[]string{"sh", "-c", "sleep 30 & exit 0"},
		StartOptions{Stdout: &stdout, WaitDelay: 25 * time.Millisecond, ProcessGroup: true},
	)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = process.KillGroup() })
	started := time.Now()
	err = process.Wait()
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Wait() took %s, want WaitDelay to close inherited pipe", elapsed)
	}
	if err == nil {
		t.Fatal("Wait() returned nil after WaitDelay closed an inherited pipe")
	}
}

// TestRealRunnerRunWaitDelayClosesInheritedPipe covers the synchronous seam
// separately from Start: RunOptions must reach exec.Cmd.WaitDelay too.
func TestRealRunnerRunWaitDelayClosesInheritedPipe(t *testing.T) {
	t.Parallel()
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	script := fmt.Sprintf("sleep 30 & echo $! > %s; exit 0", shellQuote(pidFile))
	started := time.Now()
	_, err := (RealRunner{}).Run(
		context.Background(),
		[]string{"sh", "-c", script},
		RunOptions{WaitDelay: 25 * time.Millisecond},
	)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Run() took %s, want WaitDelay to close inherited pipe", elapsed)
	}
	if err == nil {
		t.Fatal("Run() returned nil after WaitDelay closed an inherited pipe")
	}
	if raw, readErr := os.ReadFile(pidFile); readErr == nil {
		if pid, parseErr := strconv.Atoi(strings.TrimSpace(string(raw))); parseErr == nil {
			if child, findErr := os.FindProcess(pid); findErr == nil {
				_ = child.Kill()
			}
		}
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func TestRealRunnerKillGroupStopsDescendants(t *testing.T) {
	t.Parallel()
	process, err := (RealRunner{}).Start(
		context.Background(),
		[]string{"sh", "-c", "sleep 30"},
		StartOptions{ProcessGroup: true},
	)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := process.KillGroup(); err != nil {
		t.Fatalf("KillGroup() error = %v", err)
	}
	if err := process.Wait(); err == nil {
		t.Fatal("Wait() after KillGroup() returned nil, want signal status")
	}
}

func TestFakeRunnerStartDefaultsToPid4242AndNilWait(t *testing.T) {
	t.Parallel()
	fake := &FakeRunner{}
	process, err := fake.Start(context.Background(), []string{"codex", "app-server"}, StartOptions{Detach: true})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if process.Pid() != 4242 {
		t.Fatalf("Pid() = %d, want default 4242", process.Pid())
	}
	if err := process.Wait(); err != nil {
		t.Fatalf("Wait() error = %v, want nil default", err)
	}
}

func TestFakeRunnerStartRecordsCallOnItsOwnLedger(t *testing.T) {
	t.Parallel()
	fake := &FakeRunner{}
	if _, err := fake.Start(
		context.Background(),
		[]string{"pfm", "internal", "then"},
		StartOptions{Dir: "/repo", Detach: true},
	); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	starts := fake.Starts()
	if len(starts) != 1 {
		t.Fatalf("Starts() returned %d entries, want 1", len(starts))
	}
	if starts[0].Argv[0] != "pfm" || starts[0].Opts.Dir != "/repo" || !starts[0].Opts.Detach {
		t.Fatalf("Starts()[0] = %+v, want argv[0]=pfm, Dir=/repo, Detach=true", starts[0])
	}
}

func TestFakeRunnerUnscriptedInteractiveStartFailsLoudly(t *testing.T) {
	t.Parallel()
	fake := &FakeRunner{}
	_, err := fake.Start(
		context.Background(),
		[]string{"python", "worker.py"},
		StartOptions{StdinPipe: true, StdoutPipe: true},
	)
	if err == nil {
		t.Fatal("unscripted interactive Start() returned nil error")
	}
	var unscripted UnscriptedError
	if !errors.As(err, &unscripted) {
		t.Fatalf("error = %v (%T), want UnscriptedError", err, err)
	}
}

func TestFakeRunnerInteractiveLifecycleLedger(t *testing.T) {
	t.Parallel()
	fake := &FakeRunner{}
	fake.ScriptInteractive([]string{"worker"}, InteractiveScript{
		Pid:    87,
		Stdin:  discardWriteCloser{},
		Stdout: io.NopCloser(strings.NewReader("")),
	})
	process, err := fake.Start(
		context.Background(),
		[]string{"worker", "--stream"},
		StartOptions{StdinPipe: true, StdoutPipe: true, ProcessGroup: true},
	)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := process.KillGroup(); err != nil {
		t.Fatalf("KillGroup() error = %v", err)
	}
	_ = process.Wait()
	calls := fake.LifecycleCalls()
	if len(calls) != 2 || calls[0].Action != "kill-group" || calls[1].Action != "wait" {
		t.Fatalf("LifecycleCalls() = %+v, want kill-group then wait", calls)
	}
}

func TestFakeRunnerInteractiveProcessCanReleaseBlockedWrite(t *testing.T) {
	t.Parallel()
	input := &blockingWriteCloser{released: make(chan struct{})}
	fake := &FakeRunner{}
	fake.ScriptInteractive([]string{"worker"}, InteractiveScript{
		Pid:    88,
		Stdin:  input,
		Stdout: io.NopCloser(strings.NewReader("")),
	})
	process, err := fake.Start(
		context.Background(),
		[]string{"worker"},
		StartOptions{StdinPipe: true, StdoutPipe: true},
	)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	writeDone := make(chan error, 1)
	go func() {
		_, writeErr := processInput(process, []byte("blocked"))
		writeDone <- writeErr
	}()
	select {
	case <-writeDone:
		t.Fatal("write completed before pipe close")
	case <-time.After(10 * time.Millisecond):
	}
	if err := input.Close(); err != nil {
		t.Fatalf("close input: %v", err)
	}
	select {
	case <-writeDone:
	case <-time.After(time.Second):
		t.Fatal("blocked write did not release after pipe close")
	}
}

func processInput(process Process, body []byte) (int, error) {
	stdin, err := process.StdinPipe()
	if err != nil {
		return 0, err
	}
	return stdin.Write(body)
}

type blockingWriteCloser struct {
	released chan struct{}
}

type discardWriteCloser struct{}

func (discardWriteCloser) Write(body []byte) (int, error) { return len(body), nil }
func (discardWriteCloser) Close() error                   { return nil }

func (w *blockingWriteCloser) Write([]byte) (int, error) {
	<-w.released
	return 0, errors.New("input closed")
}

func (w *blockingWriteCloser) Close() error {
	select {
	case <-w.released:
	default:
		close(w.released)
	}
	return nil
}

func TestFakeRunnerScriptStartControlsPidWaitAndError(t *testing.T) {
	t.Parallel()
	fake := &FakeRunner{}
	fake.ScriptStart([]string{"claude"}, 99, errors.New("scripted wait failure"), nil)
	fake.ScriptStart([]string{"codex"}, 0, nil, errors.New("scripted start failure"))

	process, err := fake.Start(context.Background(), []string{"claude", "app-server"}, StartOptions{})
	if err != nil {
		t.Fatalf("Start(claude) error = %v", err)
	}
	if process.Pid() != 99 {
		t.Fatalf("Pid() = %d, want 99", process.Pid())
	}
	if err := process.Wait(); err == nil || err.Error() != "scripted wait failure" {
		t.Fatalf("Wait() error = %v, want %q", err, "scripted wait failure")
	}

	_, err = fake.Start(context.Background(), []string{"codex", "app-server"}, StartOptions{})
	if err == nil || err.Error() != "scripted start failure" {
		t.Fatalf("Start(codex) error = %v, want %q", err, "scripted start failure")
	}
}

// TestRealRunnerRunReportsZeroExitCodeOnSuccess is the regression for a bug
// this batch hit through internal/kill's viewport.go: RunResult.ExitCode
// answered -1 (ExitCode(nil)'s "never reached one" sentinel) even for a
// command that ran and exited 0, so a caller checking ExitCode != 0 for
// failure misread every successful run as a failure.
func TestRealRunnerRunReportsZeroExitCodeOnSuccess(t *testing.T) {
	t.Parallel()
	var runner RealRunner
	result, err := runner.Run(context.Background(), []string{"sh", "-c", "exit 0"}, RunOptions{})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0 on a successful run", result.ExitCode)
	}
}
