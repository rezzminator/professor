package gather

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// readPID reads /proc/<pid>/<element>; a failed read goes through
// procReadError.
func (proc RealProcFS) readPID(pid int, element string) ([]byte, error) {
	content, err := os.ReadFile(proc.path(pid, element))
	if err != nil {
		return nil, procReadError(err)
	}
	return content, nil
}

// procReadError keeps ESRCH, the kernel's answer to a read of a process that
// exited between the open and the read, and adds fs.ErrNotExist: callers test
// that sentinel for a gone process, as they do when the open itself misses.
func procReadError(err error) error {
	if errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("%w: %w", err, fs.ErrNotExist)
	}
	return err
}
