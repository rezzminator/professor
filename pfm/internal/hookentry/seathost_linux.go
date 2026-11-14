//go:build linux

package hookentry

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// seatTerminalPath resolves the controlling PTY even when launcher stdio is redirected.
func seatTerminalPath(tty int) (string, error) {
	device, err := unix.IoctlGetInt(tty, unix.TIOCGDEV)
	if err != nil {
		return "", fmt.Errorf("read controlling terminal device: %w", err)
	}
	path := fmt.Sprintf("/dev/pts/%d", unix.Minor(uint64(uint32(device))))
	var stat unix.Stat_t
	if err := unix.Stat(path, &stat); err != nil {
		return "", fmt.Errorf("stat controlling PTY %s: %w", path, err)
	}
	if stat.Rdev != uint64(uint32(device)) {
		return "", fmt.Errorf("controlling PTY %s device %d does not match %d", path, stat.Rdev, uint32(device))
	}
	return path, nil
}

// seatTerminalHungUp reads a PTY hangup without blocking: poll reports POLLHUP.
func seatTerminalHungUp(tty int) (bool, error) {
	terminal := []unix.PollFd{{Fd: int32(tty)}}
	if _, err := unix.Poll(terminal, 0); err != nil {
		return false, err
	}
	return terminal[0].Revents&unix.POLLHUP != 0, nil
}
