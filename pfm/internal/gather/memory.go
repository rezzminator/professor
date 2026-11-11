package gather

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Memory is the host's memory and swap, in kilobytes.
type Memory struct {
	TotalKB     uint64
	AvailableKB uint64
	SwapTotalKB uint64
	SwapFreeKB  uint64
	// AvailableErr says why AvailableKB holds no reading (its line absent or
	// malformed); nil when it does. A counter that reads 0 is not one that
	// could not be read.
	AvailableErr error
	// SwapErr says why the swap counters could not be read; they are zero.
	SwapErr error
	// Warnings name counters skipped without failing the read.
	Warnings []string
}

// HostMemory reads the host's memory counters: {root}/meminfo for a jail
// root, else the kernel's own (/proc/meminfo on Linux; hw.memsize, vm_stat
// and vm.swapusage on macOS). A failed read of the source returns its error
// unwrapped, so a caller can tell an absent source (fs.ErrNotExist).
func HostMemory(root string) (Memory, error) {
	if jail := jailRoot(root); jail != "" {
		return readLinuxMemory(jail)
	}
	return nativeHostMemory()
}

// readLinuxMemory reads a Linux meminfo. A line that does not parse is
// skipped and leaves its counter zero, except MemAvailable, whose failure
// AvailableErr names.
func readLinuxMemory(root string) (Memory, error) {
	path := filepath.Join(root, "meminfo")
	content, err := os.ReadFile(path)
	if err != nil {
		return Memory{}, err
	}
	memory := Memory{AvailableErr: fmt.Errorf("no MemAvailable line in %s", path)}
	values := map[string]*uint64{
		"MemTotal": &memory.TotalKB, "SwapTotal": &memory.SwapTotalKB, "SwapFree": &memory.SwapFreeKB,
	}
	sawAvailable := false
	for _, line := range strings.Split(string(content), "\n") {
		if !sawAvailable && strings.HasPrefix(line, "MemAvailable:") {
			sawAvailable = true
			memory.AvailableKB, memory.AvailableErr = parseMemAvailable(line, path)
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		target, found := values[strings.TrimSuffix(fields[0], ":")]
		if !found {
			continue
		}
		if value, parseErr := strconv.ParseUint(fields[1], 10, 64); parseErr == nil {
			*target = value
		}
	}
	return memory, nil
}

func parseMemAvailable(line, path string) (uint64, error) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0, fmt.Errorf("parse MemAvailable line %q in %s", line, path)
	}
	value, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse MemAvailable value in %s: %w", path, err)
	}
	return value, nil
}
