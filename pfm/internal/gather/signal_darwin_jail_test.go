//go:build jail && darwin

package gather_test

import (
	"errors"
	"os/exec"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/gather"
)

// TestPidHandleSignalsItsPid: Darwin's handle is the pid itself, so a signal
// through it reaches the process opened.
func TestPidHandleSignalsItsPid(t *testing.T) {
	child := startSleeper(t)
	handle, err := gather.NativeKernel().Open(child.Process.Pid)
	if err != nil {
		t.Fatalf("open pid handle: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if err := handle.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal through the pid handle: %v", err)
	}
	err = child.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGTERM {
		t.Fatalf("child exit = %v; want terminated by SIGTERM", err)
	}
}
