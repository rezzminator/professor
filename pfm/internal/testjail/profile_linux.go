package testjail

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// kernelProc is the real kernel's /proc. The profiler reads the counters of this
// very process, never paths.Values.ProcRoot, which a test may point at a
// fixture; /proc is a literal here for that reason alone.
const kernelProc = "/proc"

// psiDir is where the kernel publishes pressure stall information.
const psiDir = kernelProc + "/pressure"

// maxRSSKB: Linux reports ru_maxrss in kilobytes.
func maxRSSKB(v int64) int64 { return v }

// runDelaySeconds sums the time this process's threads spent runnable but not
// running (schedstat field 2, ns): the scheduler-delay signal that stays
// visible when steal time reads 0 under host starvation.
func runDelaySeconds() (float64, error) {
	tasks, err := filepath.Glob(kernelProc + "/self/task/*/schedstat")
	if err != nil || len(tasks) == 0 {
		return 0, fmt.Errorf("schedstat: no task files (%v)", err)
	}
	var total int64
	for _, path := range tasks {
		data, err := os.ReadFile(path)
		if err != nil {
			continue // a thread that exited between glob and read
		}
		fields := strings.Fields(string(data))
		if len(fields) < 2 {
			return 0, fmt.Errorf("schedstat %s: %d fields", path, len(fields))
		}
		ns, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("schedstat %s: %w", path, err)
		}
		total += ns
	}
	return float64(total) / 1e9, nil
}

// readPSI reads the VM-wide pressure stall totals under dir, in seconds, keyed
// {cpu,io,memory}_{some,full}. A missing or malformed file is an error, never
// a zero.
func readPSI(dir string) (map[string]float64, error) {
	out := map[string]float64{}
	for _, res := range []string{"cpu", "io", "memory"} {
		path := filepath.Join(dir, res)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("psi: %w", err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 5 || !strings.HasPrefix(fields[4], "total=") {
				return nil, fmt.Errorf("psi %s: malformed line %q", path, line)
			}
			us, err := strconv.ParseInt(strings.TrimPrefix(fields[4], "total="), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("psi %s: %w", path, err)
			}
			out[res+"_"+fields[0]] = float64(us) / 1e6
		}
	}
	return out, nil
}
