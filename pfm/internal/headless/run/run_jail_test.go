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
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/testjail"
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
	deadline := time.Now().Add(3 * time.Second)
	lastState := "unknown"
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		} else if err != nil {
			t.Fatalf("probe descendant process %d: %v", pid, err)
		}

		state, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
		if err == nil {
			lastState = strings.TrimSpace(string(state))
			if strings.HasPrefix(lastState, "Z") {
				return
			}
		} else {
			lastState = fmt.Sprintf("ps: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("descendant process %d remained live after cancellation (state %s)", pid, lastState)
}

// nativeStreamBudget bounds how long a test waits for the native runner's
// first streamed line, derived from the test's own deadline rather than a
// flat literal: a fixed short window turned ordinary fork/exec scheduling
// delay under a loaded suite into a failure that named nothing about
// streaming itself. A genuine streaming regression still never fires the
// signal being waited on, so no budget here lets that regression pass by
// accident — it only decides how long the failure takes to name itself.
func nativeStreamBudget(t *testing.T) time.Duration {
	t.Helper()
	budget := 30 * time.Second
	if deadline, ok := t.Deadline(); ok {
		if remaining := time.Until(deadline) - time.Second; remaining < budget {
			budget = remaining
		}
	}
	return budget
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
sleep 0.25
printf 'second\n'`)
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
	select {
	case <-writer.first:
	case <-time.After(nativeStreamBudget(t)):
		t.Fatal("native output did not stream before process completion")
	}
	select {
	case <-done:
		t.Fatal("native runner completed before the delayed output was written")
	case <-time.After(100 * time.Millisecond):
	}
	<-done
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
// checks os.Stat can observe the file mid-creation — empty — and hand the
// caller zero bytes to parse. That race was this helper's whole bug: under
// load the open/write gap widens and TestRunTimeoutKillsProcessGroup read an
// empty child-pid file ("child pid \"\": EOF") for a reason that has nothing
// to do with the timeout/kill behavior under test.
func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if info, err := os.Stat(path); err == nil && info.Size() > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("file %s did not appear with content within %s", path, timeout)
}

func TestRunTimeoutKillsProcessGroup(t *testing.T) {
	headlessJail(t)
	capture := t.TempDir()
	binary := writeEngineStub(t, `(sleep 30) &
echo "$!" > "$CAPTURE_DIR/child-pid"
sleep 30`)
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
	started := time.Now()
	go func() {
		result, err := Run(ctx, request)
		done <- outcome{result, err}
	}()
	pidPath := filepath.Join(capture, "child-pid")
	waitForFile(t, pidPath, 3*time.Second)
	ctx.arm()
	var out outcome
	select {
	case out = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run() did not return after the deadline was armed")
	}
	if out.err == nil || !errors.Is(out.err, context.DeadlineExceeded) {
		t.Fatalf("timeout err = %v, result=%#v", out.err, out.result)
	}
	if !out.result.TimedOut {
		t.Fatal("timeout result did not mark timeout")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("timeout waited for descendant pipe holder: %s", elapsed)
	}
	pidText := strings.TrimSpace(string(mustRead(t, pidPath)))
	var pid int
	if _, scanErr := fmt.Sscanf(pidText, "%d", &pid); scanErr != nil {
		t.Fatalf("child pid %q: %v", pidText, scanErr)
	}
	waitForProcessExit(t, pid)
}
