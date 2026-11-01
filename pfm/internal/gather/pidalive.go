package gather

import (
	"errors"
	"fmt"
	"syscall"
)

// ProcessAlive is the one kill -0 probe of a process: one that exists, ours or
// another user's (EPERM), is alive; ESRCH is gone; any other answer is a probe
// that could not tell, returned as an error and never read as dead. A
// non-positive pid names no single process and is refused.
func ProcessAlive(pid int) (bool, error) {
	if pid <= 0 {
		return false, fmt.Errorf("probe process: %d is not a process id", pid)
	}
	err := ProbeProcess(pid)
	switch {
	case err == nil, errors.Is(err, syscall.EPERM):
		return true, nil
	case errors.Is(err, syscall.ESRCH):
		return false, nil
	default:
		return false, fmt.Errorf("probe process %d: %w", pid, err)
	}
}

// Probe asks whether pid exists, ProbeProcess's contract; a test's fake plays
// the kernel.
type Probe func(pid int) error

// ProbeProcess is the one raw signal-0 existence probe: nil for a process that
// exists and may be signalled, ESRCH for none, EPERM for another user's. It
// tells existence only, never identity; a non-positive pid names no single
// process and is refused.
func ProbeProcess(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("probe process: %d is not a process id", pid)
	}
	return syscall.Kill(pid, 0)
}
