//go:build darwin

package harvestpy

import "golang.org/x/sys/unix"

// physicalMemoryBytes is the host's RAM, 0 when the kernel will not say.
func physicalMemoryBytes() uint64 {
	memory, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0
	}
	return memory
}
