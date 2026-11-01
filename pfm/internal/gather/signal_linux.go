//go:build linux

package gather

import (
	"errors"
	"fmt"
	"syscall"

	"golang.org/x/sys/unix"
)

// nativeKernel opens a pidfd: the descriptor names the process that held the
// pid at open, so a signal sent through it after the identity re-read reaches
// that process or fails with ESRCH, never a later process given the same pid.
type nativeKernel struct{}

func (nativeKernel) Open(pid int) (Handle, error) {
	fd, err := unix.PidfdOpen(pid, 0)
	if errors.Is(err, syscall.ENOSYS) {
		// No fallback to kill(2): without a pidfd the reuse race this door
		// exists to close is open again.
		return nil, fmt.Errorf("pidfd_open pid %d: this kernel has no pidfd (Linux 5.3+): %w", pid, err)
	}
	if err != nil {
		return nil, fmt.Errorf("pidfd_open pid %d: %w", pid, err)
	}
	return pidfd(fd), nil
}

type pidfd int

func (fd pidfd) Signal(sig syscall.Signal) error {
	return unix.PidfdSendSignal(int(fd), sig, nil, 0)
}

func (fd pidfd) Close() error {
	return unix.Close(int(fd))
}
