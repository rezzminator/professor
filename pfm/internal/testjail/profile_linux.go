package testjail

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// kernelProc is the real kernel's /proc. The profiler reads the counters of this
// very process, never paths.Values.ProcRoot, which a test may point at a
// fixture; it takes the kernel's root, never a configured one, for that reason.
var kernelProc = paths.SystemProcRoot()

// psiDir is where the kernel publishes pressure stall information.
var psiDir = kernelProc + "/pressure"

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

// userHZ is the unit of /proc/{pid}/stat's starttime, clock ticks, fixed at 100 by the Linux ABI.
const userHZ = 100

// processStart is when process pid started, to the tick: its stat starttime after /proc/stat's
// btime. Both are whole numbers, so readers agree to the second (CLOCK_BOOTTIME arithmetic, gather's
// finer reading, can straddle a second between two readers); btime follows the wall clock, so only a
// clock step between two readers moves it and splits one go test into two runs. gather owns this read for production; testjail cannot import it
// (gather's dependencies test through testjail).
func processStart(pid int) (time.Time, error) {
	stat, err := os.ReadFile(fmt.Sprintf("%s/%d/stat", kernelProc, pid))
	if err != nil {
		return time.Time{}, err
	}
	kernel, err := os.ReadFile(kernelProc + "/stat")
	if err != nil {
		return time.Time{}, err
	}
	return parseProcessStart(string(stat), string(kernel))
}

// parseProcessStart reads starttime (field 22) from a /proc/{pid}/stat line, whose command name
// (field 2, in parentheses) may hold spaces and parentheses, and btime from /proc/stat's text.
func parseProcessStart(stat, kernel string) (time.Time, error) {
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return time.Time{}, fmt.Errorf("stat %q: no command name in parentheses", stat)
	}
	fields := strings.Fields(stat[end+1:]) // fields[0] is field 3, the state
	if len(fields) < 20 {
		return time.Time{}, fmt.Errorf(
			"stat %q: %d fields after the command name, want starttime at the 20th",
			stat,
			len(fields),
		)
	}
	ticks, err := strconv.ParseInt(fields[19], 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("stat starttime: %w", err)
	}
	for _, line := range strings.Split(kernel, "\n") {
		if value, found := strings.CutPrefix(line, "btime "); found {
			boot, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
			if err != nil {
				return time.Time{}, fmt.Errorf("%s/stat btime: %w", kernelProc, err)
			}
			return time.Unix(boot, 0).Add(time.Duration(ticks) * (time.Second / userHZ)), nil
		}
	}
	return time.Time{}, fmt.Errorf("%s/stat: no btime line", kernelProc)
}
