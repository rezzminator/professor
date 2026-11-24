package tmux

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

// TestExecRunsThroughTheContextsRunner: under WithClientRunner a call is the
// runner's — the default binary is named, never a host path, the argv is
// tmux's own, stdin and Stderr travel — and a scripted exit surfaces as the
// façade's error unchanged, recorded with its code, whose stderr ExitStderr reads.
func TestExecRunsThroughTheContextsRunner(t *testing.T) {
	ctx, recorder := obs.Test(t)
	fake := &deps.FakeRunner{}
	fake.Script([]string{"tmux", "-S", "/tmp/s/sock", "list-panes"},
		deps.RunResult{Stderr: []byte("no server running on /tmp/s/sock\n"), ExitCode: 1}, nil)
	fake.Script([]string{"tmux", "-S", "/tmp/s/sock", "load-buffer"}, deps.RunResult{}, nil)
	ctx = WithClientRunner(ctx, fake)

	_, err := Exec(ctx, "", "/tmp/s/sock", "list-panes", "-a").Output()
	var exit *ExitError
	if !errors.As(err, &exit) || err.Error() != "exit status 1" || exit.ExitCode() != 1 ||
		string(exit.Stderr) != "no server running on /tmp/s/sock\n" {
		t.Fatalf("Output() error = %#v, want *ExitError{1, the stderr} reading \"exit status 1\"", err)
	}
	if stderr, ok := ExitStderr(err); !ok || string(stderr) != "no server running on /tmp/s/sock\n" {
		t.Fatalf("ExitStderr(%v) = %q, %v; want the scripted stderr, true", err, stderr, ok)
	}
	if stderr, ok := ExitStderr(context.Canceled); ok || stderr != nil {
		t.Fatalf("ExitStderr(context.Canceled) = %q, %v; want nil, false", stderr, ok)
	}
	load := Exec(ctx, "", "/tmp/s/sock", "load-buffer", "-b", "buf", "-")
	load.Stdin = strings.NewReader("body")
	if err := load.Run(); err != nil {
		t.Fatalf("Run() = %v, want nil", err)
	}
	calls := fake.Calls()
	want := [][]string{
		{"tmux", "-S", "/tmp/s/sock", "list-panes", "-a"},
		{"tmux", "-S", "/tmp/s/sock", "load-buffer", "-b", "buf", "-"},
	}
	if len(calls) != 2 || !reflect.DeepEqual(calls[0].Argv, want[0]) || !reflect.DeepEqual(calls[1].Argv, want[1]) {
		t.Fatalf("runner calls = %+v, want argv %q", calls, want)
	}
	if string(calls[1].Opts.Stdin) != "body" {
		t.Fatalf("stdin = %q, want %q", calls[1].Opts.Stdin, "body")
	}
	if exitField, _ := recorder.Records()[0].Field(obs.FieldExit); exitField != float64(1) {
		t.Fatalf("recorded exit = %v, want 1: %s", exitField, recorder.Raw())
	}
}

// TestExecEndsAHandledHangAtItsDeadline: a handler that waits on the context
// — a stopped server — makes Run return at the call's deadline, TimedOut.
func TestExecEndsAHandledHangAtItsDeadline(t *testing.T) {
	hanging := hangingTmux()
	deadline, expire := testwait.Deadline(t)
	command := Exec(WithClientRunner(deadline, hanging), "", "/tmp/s/sock", "list-panes", "-a")
	ran := make(chan error, 1)
	go func() { ran <- command.Run() }()
	reached(t, hanging)
	expire()
	err, _ := testwait.Receive(t, "the hung call to end at its deadline", ran)
	if !errors.Is(err, context.DeadlineExceeded) || !command.TimedOut() {
		t.Fatalf("Run() = %v (TimedOut %v), want context.DeadlineExceeded and TimedOut", err, command.TimedOut())
	}
}
