//go:build jail && linux

package gather_test

import (
	"errors"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/gather"
)

// TestPidfdOutlivesItsProcess: a pidfd opened on a child keeps naming that
// child after it is reaped, so a signal through it fails with ESRCH instead of
// reaching whatever process the kernel gives the pid next.
func TestPidfdOutlivesItsProcess(t *testing.T) {
	child := startSleeper(t)
	handle, err := gather.NativeKernel().Open(child.Process.Pid)
	if err != nil {
		t.Fatalf("open pidfd: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if err := child.Process.Kill(); err != nil {
		t.Fatalf("kill child: %v", err)
	}
	_ = child.Wait()
	if err := handle.Signal(syscall.SIGTERM); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("signal through a reaped child's pidfd = %v; want ESRCH", err)
	}
}
