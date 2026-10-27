//go:build darwin

package hookentry

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func seatForeground(tty, group int) error {
	return unix.IoctlSetPointerInt(tty, unix.TIOCSPGRP, group)
}

// seatTerminalPath uses the kernel's controlling device, not redirected launcher fds.
func seatTerminalPath(tty int) (string, error) {
	group, err := unix.IoctlGetInt(tty, unix.TIOCGPGRP)
	if err != nil {
		return "", fmt.Errorf("read controlling terminal group: %w", err)
	}
	process, err := unix.SysctlKinfoProc("kern.proc.pid", os.Getpid())
	if err != nil {
		return "", fmt.Errorf("read controlling terminal device: %w", err)
	}
	if process.Eproc.Tpgid != int32(group) || process.Eproc.Tdev == -1 {
		return "", fmt.Errorf(
			"controlling terminal identity changed (device %d, group %d/%d)",
			process.Eproc.Tdev,
			process.Eproc.Tpgid,
			group,
		)
	}
	paths, err := filepath.Glob("/dev/ttys*")
	if err != nil {
		return "", fmt.Errorf("enumerate pane terminals: %w", err)
	}
	for _, path := range paths {
		var stat unix.Stat_t
		if err := unix.Stat(path, &stat); err != nil {
			if os.IsNotExist(err) {
				continue // Another pane's terminal disappeared during enumeration.
			}
			return "", fmt.Errorf("stat pane terminal %s: %w", path, err)
		}
		if stat.Rdev == process.Eproc.Tdev {
			return path, nil
		}
	}
	return "", fmt.Errorf("no concrete PTY for controlling device %d", process.Eproc.Tdev)
}
