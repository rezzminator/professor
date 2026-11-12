//go:build darwin

package hookentry

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

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

// seatTerminalHungUp reads a PTY hangup without blocking. Darwin's poll reports
// POLLHUP only through a read filter (Linux reports it unasked), so a one-shot
// kqueue read filter asks directly: EV_EOF on it is the hangup.
func seatTerminalHungUp(tty int) (bool, error) {
	queue, err := unix.Kqueue()
	if err != nil {
		return false, fmt.Errorf("open kqueue: %w", err)
	}
	defer unix.Close(queue)
	changes := []unix.Kevent_t{{Ident: uint64(tty), Filter: unix.EVFILT_READ, Flags: unix.EV_ADD | unix.EV_ONESHOT}}
	events := make([]unix.Kevent_t, 1)
	count, err := unix.Kevent(queue, changes, events, &unix.Timespec{})
	if err != nil {
		return false, fmt.Errorf("watch terminal %d: %w", tty, err)
	}
	if count == 0 {
		return false, nil
	}
	if events[0].Flags&unix.EV_ERROR != 0 {
		return false, fmt.Errorf("watch terminal %d: %w", tty, syscall.Errno(events[0].Data))
	}
	return events[0].Flags&unix.EV_EOF != 0, nil
}
