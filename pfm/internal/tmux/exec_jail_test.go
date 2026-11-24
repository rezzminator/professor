//go:build jail

package tmux

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

// TestExecBoundsACallWhoseContextHasNoDeadline: a tmux that never answers —
// a stopped server's client blocks exactly so — is killed at the door's
// default bound and the call returns its error, never hangs its caller.
func TestExecBoundsACallWhoseContextHasNoDeadline(t *testing.T) {
	// The client marks that it started, then opens a FIFO no one ever writes:
	// it never answers, and the bound is timed on a fake the test moves.
	dir := t.TempDir()
	never := filepath.Join(dir, "never-written")
	if err := syscall.Mkfifo(never, 0o600); err != nil {
		t.Fatalf("make the unanswered FIFO %s: %v", never, err)
	}
	started := filepath.Join(dir, "started")
	binary := filepath.Join(dir, "tmux-hung")
	script := "#!/bin/sh\n: > '" + started + "'\nexec cat '" + never + "'\n"
	if err := testjail.WriteExecutable(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	fake := clock.NewFake(time.Unix(0, 0))
	command := Exec(WithBoundClock(context.Background(), fake), binary, "socket", "display-message", "-p", "#{pid}")
	returned := make(chan error, 1)
	go func() {
		_, err := command.Output()
		returned <- err
	}()
	testwait.Until(t, "the unanswered client to start ("+started+")", func() (bool, string) {
		_, err := os.Stat(started)
		return err == nil, fmt.Sprintf("stat %s: %v", started, err)
	})
	if pending := fake.Pending(); pending != 1 {
		t.Fatalf("armed bounds = %d (%s), want the one default bound", pending, fake)
	}
	fake.Advance(defaultTimeout - time.Nanosecond)
	if err := command.ctx.Err(); err != nil {
		t.Fatalf("call ended %s before its %s bound: %v", time.Nanosecond, defaultTimeout, err)
	}
	fake.Advance(time.Nanosecond)
	err, _ := testwait.Receive(t, "the unanswered call to end at its default bound", returned)
	if err == nil {
		t.Fatal("a tmux that never answered returned no error")
	}
	if command.ProcessState == nil || command.ProcessState.Exited() {
		t.Fatalf("client state %v, want it killed at the bound", command.ProcessState)
	}
	if !command.TimedOut() {
		t.Fatalf("TimedOut() = false after the bound ended the call (err %v), want true", err)
	}
}

// TestExecAttachedOutlivesTheDefaultBound: an attach lasts as long as its user
// keeps it, so the default bound that ends an unanswered read never ends it.
// Both calls are timed on one fake moved past the default: the bounded call
// ends at it, the attach arms no bound and runs to its own exit.
func TestExecAttachedOutlivesTheDefaultBound(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "tmux-attached")
	if err := testjail.WriteExecutable(binary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	fake := clock.NewFake(time.Unix(0, 0))
	ctx := WithBoundClock(context.Background(), fake)

	bounded := Exec(ctx, binary, "socket", "display-message")
	fake.Advance(defaultTimeout)
	if err := bounded.Run(); err == nil || !bounded.TimedOut() {
		t.Fatalf(
			"bounded call past the default = %v (timed out %v), want it ended by the bound",
			err,
			bounded.TimedOut(),
		)
	}
	command := ExecAttached(ctx, binary, "socket", "attach")
	if pending := fake.Pending(); pending != 0 {
		t.Fatalf("an attach armed %d bounds (%s), want none", pending, fake)
	}
	fake.Advance(defaultTimeout)
	if err := command.Run(); err != nil || command.TimedOut() {
		t.Fatalf(
			"attach past the %s default bound = %v (timed out %v), want it to run to its own exit",
			defaultTimeout, err, command.TimedOut(),
		)
	}
}

// TestExecKeepsCommandsContract: the terminal is Command plus completion —
// same binary, same -S socket argv, same cleared TMUX, and the embedded
// *exec.Cmd still takes Stdin and exposes Process for the façades that
// set or kill them. Its subject is the real child the embed starts, so it
// runs one (a fake runner starts no Process).
func TestExecKeepsCommandsContract(t *testing.T) {
	t.Setenv("TMUX", "/tmp/caller-server,1,0")
	binary := printingTmux(t)
	command := Exec(context.Background(), binary, "/sockets/cc-1", "load-buffer", "-b", "buf", "-")
	if command.Path != binary || command.Args[1] != "-S" || command.Args[2] != "/sockets/cc-1" {
		t.Fatalf("argv = %q", command.Args)
	}
	command.Stdin = strings.NewReader("body")
	if err := command.Run(); err != nil {
		t.Fatal(err)
	}
	if command.Process == nil {
		t.Fatal("the embedded exec.Cmd lost its Process")
	}
}

// printingTmux writes a shell script that plays tmux: it prints its arguments
// and exits 0. Its name is not tmux, so the test-process refusal lets it run.
func printingTmux(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "tmux-fake")
	if err := testjail.WriteExecutable(binary, []byte("#!/bin/sh\nprintf '%s ' \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return binary
}
