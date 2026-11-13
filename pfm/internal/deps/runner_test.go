package deps

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

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
