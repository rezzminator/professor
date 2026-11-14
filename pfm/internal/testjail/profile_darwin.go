package testjail

import (
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

// maxRSSKB: Darwin reports ru_maxrss in bytes.
func maxRSSKB(v int64) int64 { return v / 1024 }

// runDelaySeconds: Darwin has no per-thread scheduler-delay counter.
func runDelaySeconds() (float64, error) {
	return 0, errors.New("run delay: not available on darwin")
}

// psiDir is unused on Darwin: it has no pressure stall information.
const psiDir = ""

// readPSI: Darwin has no pressure stall information.
func readPSI(string) (map[string]float64, error) {
	return nil, errors.New("psi: not available on darwin")
}

// processStart is process pid's p_starttime, the wall-clock microsecond the kernel created it.
// gather owns this read for production; testjail cannot import it (gather's dependencies test
// through testjail).
func processStart(pid int) (time.Time, error) {
	process, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return time.Time{}, fmt.Errorf("sysctl kern.proc.pid %d: %w", pid, err)
	}
	started := process.Proc.P_starttime
	return time.Unix(int64(started.Sec), int64(started.Usec)*int64(time.Microsecond)), nil
}
