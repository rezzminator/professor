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
	err := syscall.Kill(pid, 0)
	switch {
	case err == nil, errors.Is(err, syscall.EPERM):
		return true, nil
	case errors.Is(err, syscall.ESRCH):
		return false, nil
	default:
		return false, fmt.Errorf("probe process %d: %w", pid, err)
	}
}
