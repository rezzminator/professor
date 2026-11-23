//go:build jail && linux

package index

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// indexStressPeakRSS is the calling process's own peak resident memory in
// bytes: VmHWM of /proc/self/status. The peak a parent reads from a child's
// wait status starts from the parent's own, since exec keeps the replaced
// image's high-water mark, and getrusage of the child itself carries it too.
func indexStressPeakRSS() (int64, error) {
	const statusPath = "/proc/self/status"
	status, err := os.ReadFile(statusPath)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", statusPath, err)
	}
	for _, line := range strings.Split(string(status), "\n") {
		value, found := strings.CutPrefix(line, "VmHWM:")
		if !found {
			continue
		}
		kib, err := strconv.ParseInt(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(value), "kB")), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse %s VmHWM %q: %w", statusPath, value, err)
		}
		return kib << 10, nil
	}
	return 0, fmt.Errorf("%s holds no VmHWM line:\n%s", statusPath, status)
}
