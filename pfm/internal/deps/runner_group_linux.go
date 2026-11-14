package deps

import (
	"errors"
	"syscall"
)

// GroupGone reports whether err, a signal to process group pgid refused, means the group holds
// nothing left to signal: Linux answers ESRCH once the last member is reaped, and signals a group of
// exited, unreaped members without an error.
func GroupGone(_ int, err error) bool { return errors.Is(err, syscall.ESRCH) }
