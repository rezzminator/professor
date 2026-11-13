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
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
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

// The platform gate: RealRunner.LookPath is Resolve, and Run and Start pass a
// bare argv[0] through the same gate, so a registry name gated to another
// platform is refused before exec even when a binary of that name sits on
// PATH. A name on PATH outside the registry resolves and runs, and a path
// (it holds '/') is never gated, so a test's stub runs.
func TestRealRunnerGatesOffPlatformRegistryNames(t *testing.T) {
	offPlatform := map[string]string{"linux": "launchctl", "darwin": "getent"}[runtime.GOOS]
	if offPlatform == "" {
		t.Fatalf("no off-platform registry name pinned for %s", runtime.GOOS)
	}
	directory := t.TempDir()
	staged := writeExitZeroScript(t, filepath.Join(directory, "pfm-lookpath-probe"))
	stub := writeExitZeroScript(t, filepath.Join(directory, offPlatform))
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	want := "dependency " + offPlatform + " is not supported on " + runtime.GOOS

	var runner RealRunner
	path, err := runner.LookPath("pfm-lookpath-probe")
	if err != nil || path != staged {
		t.Fatalf("LookPath(pfm-lookpath-probe) = %q, %v; want %q, nil", path, err, staged)
	}
	path, err = runner.LookPath(offPlatform)
	if err == nil || err.Error() != want {
		t.Fatalf("LookPath(%s) = %q, %v; want the platform refusal %q", offPlatform, path, err, want)
	}

	result, err := runner.Run(context.Background(), []string{offPlatform, "print"}, RunOptions{})
	if err == nil || err.Error() != want || result.ExitCode != -1 {
		t.Fatalf("Run(%s print) = %+v, %v; want ExitCode -1 and %q", offPlatform, result, err, want)
	}
	result, err = runner.Run(context.Background(), []string{"sh", "-c", "exit 7"}, RunOptions{})
	if err != nil || result.ExitCode != 7 {
		t.Fatalf("Run(sh -c 'exit 7') = %+v, %v; want ExitCode 7, nil", result, err)
	}
	result, err = runner.Run(context.Background(), []string{stub, "print"}, RunOptions{})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("Run(%s print) = %+v, %v; want the path stub to run: ExitCode 0, nil", stub, result, err)
	}

	process, err := runner.Start(context.Background(), []string{offPlatform, "print"}, StartOptions{})
	if err == nil || err.Error() != want || process != nil {
		t.Fatalf("Start(%s print) = %v, %v; want nil and %q", offPlatform, process, err, want)
	}
	process, err = runner.Start(context.Background(), []string{stub, "print"}, StartOptions{})
	if err != nil {
		t.Fatalf("Start(%s print) error = %v; want the path stub to start", stub, err)
	}
	if err := process.Wait(); err != nil {
		t.Fatalf("Start(%s print).Wait() = %v; want nil", stub, err)
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

// A Detach start leads its own session, hands the child its ExtraFiles from
// descriptor 3, refuses Wait by name, and reaps the child once it exits.
func TestRealRunnerStartDetachReleasesAndWaitNamesIt(t *testing.T) {
	t.Parallel()
	extra, err := os.Create(filepath.Join(t.TempDir(), "extra"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = extra.Close() }()
	process, err := RealRunner{}.Start(
		context.Background(),
		[]string{"/bin/sh", "-c", "echo on-fd-3 >&3; exec sleep 30"},
		StartOptions{Detach: true, ExtraFiles: []*os.File{extra}},
	)
	if err != nil {
		t.Fatalf("Start(Detach) error = %v", err)
	}
	pid := process.Pid()
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	if session, err := unix.Getsid(pid); err != nil || session != pid {
		t.Fatalf("Getsid(%d) = %d, %v; want the detached child to lead its own session", pid, session, err)
	}
	want := fmt.Sprintf(
		`deps: Process.Wait: "/bin/sh" (pid %d) was started detached and released; it cannot be waited on`,
		pid,
	)
	if err := process.Wait(); err == nil || err.Error() != want {
		t.Fatalf("Wait() = %v, want %q", err, want)
	}
	if err := process.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	deadline := time.Now().Add(6 * time.Second)
	for {
		body, err := os.ReadFile(extra.Name())
		if err == nil && string(body) == "on-fd-3\n" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s = %q, %v; want the child's fd 3 line %q", extra.Name(), body, err, "on-fd-3\n")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatalf("stop detached pid %d: %v", pid, err)
	}
	// A long-lived caller (the paced statusline worker) must not collect a
	// zombie per detached child: once the child exits, its pid is reaped.
	deadline = time.Now().Add(6 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("detached pid %d still in the process table 6s after it exited: never reaped (zombie)", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// SignalGroup reaches a live group with the signal asked for, answers nil
// once the group is gone, and refuses a child that has no group of its own.
func TestRealRunnerSignalGroup(t *testing.T) {
	t.Parallel()
	live, err := RealRunner{}.Start(
		context.Background(),
		[]string{"sh", "-c", "sleep 30"},
		StartOptions{ProcessGroup: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := live.SignalGroup(syscall.SIGTERM); err != nil {
		t.Fatalf("SignalGroup(SIGTERM) on a live group = %v, want nil", err)
	}
	var exitErr *exec.ExitError
	if err := live.Wait(); !errors.As(err, &exitErr) ||
		exitErr.Sys().(syscall.WaitStatus).Signal() != syscall.SIGTERM {
		t.Fatalf("Wait() after SignalGroup(SIGTERM) = %v, want the child terminated by SIGTERM", err)
	}
	if err := live.SignalGroup(syscall.SIGTERM); err != nil {
		t.Fatalf("SignalGroup(SIGTERM) on an exited group = %v, want nil", err)
	}
	ungrouped, err := RealRunner{}.Start(context.Background(), []string{"sh", "-c", "exit 0"}, StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := ungrouped.Wait(); err != nil {
		t.Fatal(err)
	}
	const want = "deps: process group was not requested"
	if err := ungrouped.SignalGroup(syscall.SIGTERM); err == nil || err.Error() != want {
		t.Fatalf("SignalGroup on an ungrouped child = %v, want %q", err, want)
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

// pidGone polls up to five seconds for pid to disappear. The fence container
// runs with an init that reaps orphans, so a killed process leaves no zombie.
func pidGone(pid int) bool {
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestRealRunnerProcessGroupDeadlineKillsBackgroundedDescendants(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	t.Cleanup(func() {
		if raw, err := os.ReadFile(pidFile); err == nil {
			if pid, convErr := strconv.Atoi(strings.TrimSpace(string(raw))); convErr == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := RealRunner{}.Run(
			ctx,
			[]string{"sh", "-c", `sleep 300 & echo $! > "$1"; exec sleep 300`, "sh", pidFile},
			// WaitDelay only bounds a broken run: with the group killed, nothing
			// holds the pipes and Run returns at the deadline.
			RunOptions{ProcessGroup: true, WaitDelay: 3 * time.Second},
		)
		finished <- err
	}()
	select {
	case <-finished:
	case <-time.After(15 * time.Second):
		t.Fatal("Run did not return within 15s of its 500ms deadline")
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("the backgrounded child never recorded its pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("parse recorded pid %q: %v", raw, err)
	}
	if !pidGone(pid) {
		t.Fatalf("backgrounded descendant %d survived the deadline", pid)
	}
}

func TestRealRunnerStartCanonicalArgvAndForeground(t *testing.T) {
	for _, opts := range []StartOptions{{Foreground: true, Detach: true}, {Foreground: true, Ctty: -1}} {
		process, err := (RealRunner{}).Start(context.Background(), []string{"sh", "-c", "exit 0"}, opts)
		if err == nil {
			if !opts.Detach {
				_ = process.Wait()
			}
			t.Errorf("invalid foreground options accepted: %+v", opts)
		}
	}
	var stdout bytes.Buffer
	process, err := (RealRunner{}).Start(
		context.Background(),
		[]string{"sh", "-c", `printf '%s' "$0"`},
		StartOptions{Argv0: "canonical-agent", Stdout: &stdout},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Wait(); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "canonical-agent" {
		t.Errorf("child argv0 = %q, want canonical-agent", stdout.String())
	}
	for _, tc := range []struct {
		name                       string
		opts                       StartOptions
		foreground, group, session bool
		tty                        int
	}{
		{name: "ordinary"},
		{name: "group", opts: StartOptions{ProcessGroup: true}, group: true},
		{name: "detached", opts: StartOptions{Detach: true}, session: true},
		{name: "foreground", opts: StartOptions{Foreground: true, Ctty: 7}, foreground: true, group: true, tty: 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := startProcessAttributes(tc.opts)
			if got == nil {
				if tc.foreground || tc.group || tc.session {
					t.Fatalf("no process attributes for %+v", tc.opts)
				}
				return
			}
			if got.Foreground != tc.foreground || got.Setpgid != tc.group || got.Setsid != tc.session ||
				got.Ctty != tc.tty {
				t.Fatalf(
					"process attributes = %+v, want foreground=%v group=%v session=%v tty=%d",
					got,
					tc.foreground,
					tc.group,
					tc.session,
					tc.tty,
				)
			}
		})
	}
}

func writeExitZeroScript(t *testing.T, path string) string {
	t.Helper()
	if err := writeExecutableUnderForkLock(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
