//go:build jail

package run

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

func writeEngineStub(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "engine")
	script := "#!/bin/sh\nset -eu\n" + body + "\n"
	if err := testjail.WriteExecutable(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func waitForProcessExit(t *testing.T, pid int) {
	t.Helper()
	testwait.Until(t, fmt.Sprintf("descendant process %d to exit after cancellation", pid), func() (bool, string) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return true, ""
		} else if err != nil {
			t.Fatalf("probe descendant process %d: %v", pid, err)
		}
		state, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
		if err != nil {
			return false, fmt.Sprintf("ps: %v", err)
		}
		lastState := strings.TrimSpace(string(state))
		return strings.HasPrefix(lastState, "Z"), "state " + lastState
	})
}

func TestRunNativeStreamsStdinAndPreservesUnknownArgs(t *testing.T) {
	headlessJail(t)
	capture := t.TempDir()
	userSchema := filepath.Join(capture, "user-schema.json")
	if err := os.WriteFile(userSchema, []byte(`{"type":"object"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := writeEngineStub(t, `cat > "$CAPTURE_DIR/stdin"
printf '%s\n' "$@" > "$CAPTURE_DIR/args"
printf 'first\n'
read -r _ < "$CAPTURE_DIR/release"
printf 'second\n'`)
	release := filepath.Join(capture, "release")
	if err := syscall.Mkfifo(release, 0o600); err != nil {
		t.Fatal(err)
	}
	writer := &observedWriter{first: make(chan struct{})}
	request := Request{
		Config: claudeMachine(binary, filepath.Join(t.TempDir(), "cc")), Engine: pfmengine.Claude,
		Prompt: "unused", Native: true, Env: testEnv(capture), Stdin: strings.NewReader("streamed input\n"),
		Stdout: writer, Args: []string{"--future-flag", "value with spaces", "--output-schema", userSchema},
	}
	done := make(chan struct{})
	var result Result
	var runErr error
	go func() {
		result, runErr = Run(context.Background(), request)
		close(done)
	}()
	testwait.Receive(t, "native output to stream before process completion", writer.first)
	select {
	case <-done:
		t.Fatal("native runner completed before the delayed output was written")
	default:
	}
	if err := os.WriteFile(release, []byte("go\n"), 0o600); err != nil {
		t.Fatalf("release the engine stub: %v", err)
	}
	testwait.Receive(t, "the native runner to complete once the stub was released", done)
	if runErr != nil {
		t.Fatalf("Run() error = %v", runErr)
	}
	if got := writer.String(); got != "first\nsecond\n" {
		t.Fatalf("streamed output = %q", got)
	}
	if got := string(mustRead(t, filepath.Join(capture, "stdin"))); got != "streamed input\n" {
		t.Fatalf("stdin = %q", got)
	}
	args := capturedLines(t, filepath.Join(capture, "args"))
	if !containsPair(args, "--future-flag", "value with spaces") {
		t.Fatalf("unknown native args were not preserved: %#v", args)
	}
	if !containsPair(args, "--output-schema", userSchema) {
		t.Fatalf("native user schema arg was not preserved: %#v", args)
	}
	if _, err := os.Stat(userSchema); err != nil {
		t.Fatalf("native user schema was removed: %v", err)
	}
	if result.Answer != "first\nsecond\n" {
		t.Fatalf("native answer = %q", result.Answer)
	}
}

// waitForFile waits for path to exist AND be non-empty. Existence alone is
// not enough: the shell redirection that creates it (`echo "$!" > path`)
// opens/truncates the file before the write lands, so a poll that only
// checks os.Stat can observe the file mid-creation, empty, and hand the
// caller zero bytes to parse.
func waitForFile(t *testing.T, path string) {
	t.Helper()
	testwait.Until(t, "file "+path+" to appear with content", func() (bool, string) {
		info, err := os.Stat(path)
		if err != nil {
			return false, err.Error()
		}
		return info.Size() > 0, "empty file"
	})
}

func TestRunTimeoutKillsProcessGroup(t *testing.T) {
	headlessJail(t)
	capture := t.TempDir()
	binary := writeEngineStub(t, `(`+testwait.Hold+` >/dev/null) &
echo "$!" > "$CAPTURE_DIR/child-pid"
`+testwait.Hold)
	request := Request{
		Config: claudeMachine(binary, filepath.Join(t.TempDir(), "cc")), Engine: pfmengine.Claude, Prompt: "hello",
		Env: testEnv(capture),
	}
	ctx := newArmedDeadlineContext()
	type outcome struct {
		result Result
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := Run(ctx, request)
		done <- outcome{result, err}
	}()
	pidPath := filepath.Join(capture, "child-pid")
	waitForFile(t, pidPath)
	ctx.arm()
	out, _ := testwait.Receive(t, "Run() to return after the deadline was armed", done)
	if out.err == nil || !errors.Is(out.err, context.DeadlineExceeded) {
		t.Fatalf("timeout err = %v, result=%#v", out.err, out.result)
	}
	if !out.result.TimedOut {
		t.Fatal("timeout result did not mark timeout")
	}
	pidText := strings.TrimSpace(string(mustRead(t, pidPath)))
	var pid int
	if _, scanErr := fmt.Sscanf(pidText, "%d", &pid); scanErr != nil {
		t.Fatalf("child pid %q: %v", pidText, scanErr)
	}
	waitForProcessExit(t, pid)
}
