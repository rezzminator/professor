//go:build darwin

package gather

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// nativeKernel signals by pid: Darwin has no pidfd. The door re-reads the
// process's identity immediately before kill(2), so the residual window is the
// few microseconds between that sysctl read and the kill: the target would
// have to exit, be reaped, and its pid be handed to a new process of the same
// user inside it. Darwin hands out pids sequentially up to 99999 before
// wrapping, which makes that window practically unreachable, but it is not
// closed the way a Linux pidfd closes it.
type nativeKernel struct{}

func (nativeKernel) Open(pid int) (Handle, error) {
	return pidHandle(pid), nil
}

type pidHandle int

func (pid pidHandle) Signal(sig syscall.Signal) error {
	return unix.Kill(int(pid), sig)
}

func (pidHandle) Close() error { return nil }
