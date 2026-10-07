//go:build linux

package harvestpy

import "golang.org/x/sys/unix"

// physicalMemoryBytes is the host's RAM, 0 when the kernel will not say.
func physicalMemoryBytes() uint64 {
	var info unix.Sysinfo_t
	if err := unix.Sysinfo(&info); err != nil {
		return 0
	}
	return info.Totalram * uint64(info.Unit)
}
