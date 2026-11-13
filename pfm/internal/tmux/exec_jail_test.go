package tmux

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestExecBoundsACallWhoseContextHasNoDeadline: a tmux that never answers —
// a stopped server's client blocks exactly so — is killed at the door's
// default bound and the call returns its error, never hangs its caller.
func TestExecBoundsACallWhoseContextHasNoDeadline(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "tmux-hung")
	if err := testjail.WriteExecutable(binary, []byte("#!/bin/sh\nexec sleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	saved := defaultTimeout
	defaultTimeout = 200 * time.Millisecond
	t.Cleanup(func() { defaultTimeout = saved })

	started := time.Now()
	command := Exec(context.Background(), binary, "socket", "display-message", "-p", "#{pid}")
	_, err := command.Output()
	if err == nil {
		t.Fatal("a tmux that never answered returned no error")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("the unanswered call returned after %s, want it ended at the %s bound", elapsed, defaultTimeout)
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
func TestExecAttachedOutlivesTheDefaultBound(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "tmux-attached")
	if err := testjail.WriteExecutable(binary, []byte("#!/bin/sh\nexec sleep 0.6\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	saved := defaultTimeout
	defaultTimeout = 200 * time.Millisecond
	t.Cleanup(func() { defaultTimeout = saved })

	command := ExecAttached(context.Background(), binary, "socket", "attach")
	if err := command.Run(); err != nil || command.TimedOut() {
		t.Fatalf(
			"attach past the %s default bound = %v (timed out %v), want it to run to its own exit",
			defaultTimeout, err, command.TimedOut(),
		)
	}
}
