package gather_test

import (
	"context"
	"errors"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/gather"
)

// startSleeper starts a child this test owns and kills it on exit.
func startSleeper(t *testing.T) *exec.Cmd {
	t.Helper()
	child := exec.Command("sleep", "300")
	if err := child.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	t.Cleanup(func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	})
	return child
}

// TestNativeDoorOnALiveChild runs the real door (pidfd on Linux) on a child: a
// captured identity that no longer matches gets nothing, the matching one is
// stopped by SIGTERM, and the reaped child's identity is gone.
func TestNativeDoorOnALiveChild(t *testing.T) {
	child := startSleeper(t)
	door := gather.NewDoor(gather.NewProcFS(""))
	captured, err := gather.IdentifyProcess(door.Procs, child.Process.Pid)
	if err != nil {
		t.Fatalf("IdentifyProcess(child): %v", err)
	}
	reused := captured
	reused.Start++
	if got, err := door.Signal(
		gather.Target{Identity: reused},
		syscall.SIGTERM,
	); err != nil ||
		got != gather.OutcomeGone {
		t.Fatalf("Signal(later start) = %v, %v; want %v", got, err, gather.OutcomeGone)
	}
	changed := captured
	changed.Argv = []string{"sleep", "301"}
	if got, err := door.Signal(
		gather.Target{Identity: changed},
		syscall.SIGTERM,
	); err != nil ||
		got != gather.OutcomeChanged {
		t.Fatalf("Signal(other argv) = %v, %v; want %v", got, err, gather.OutcomeChanged)
	}
	if alive, err := door.Alive(captured); err != nil || !alive {
		t.Fatalf("Alive(child) after withheld signals = %v, %v; want alive", alive, err)
	}
	esc := gather.Escalation{Term: 10 * time.Second, Kill: 10 * time.Second, Poll: 20 * time.Millisecond}
	stopped, err := door.Stop(context.Background(), gather.Target{Identity: captured}, esc)
	if err != nil || stopped != gather.StoppedOnTerm {
		t.Fatalf("Stop(child) = %v, %v; want %v", stopped, err, gather.StoppedOnTerm)
	}
	err = child.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGTERM {
		t.Fatalf("child exit = %v; want terminated by SIGTERM", err)
	}
	if got, err := door.Signal(
		gather.Target{Identity: captured},
		syscall.SIGKILL,
	); err != nil ||
		got != gather.OutcomeGone {
		t.Fatalf("Signal(reaped child) = %v, %v; want %v", got, err, gather.OutcomeGone)
	}
}
