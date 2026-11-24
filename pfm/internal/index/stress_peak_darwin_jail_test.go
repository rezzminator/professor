//go:build jail && darwin

package index

import (
	"fmt"
	"syscall"
)

// indexStressPeakRSS is the calling process's own peak resident memory in
// bytes: getrusage of the process itself, whose Maxrss darwin counts in bytes.
func indexStressPeakRSS() (int64, error) {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return 0, fmt.Errorf("getrusage self: %w", err)
	}
	return usage.Maxrss, nil
}
