//go:build journey

package journey

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// cgroupDir is the journey machine's own cgroup: run.sh boots it with a private cgroup namespace, so
// the root of the hierarchy it sees is the whole machine.
const cgroupDir = "/sys/fs/cgroup"

// machineMeter reads what the machine spent while the suite ran: CPU from the cgroup's cpu.stat, and
// memory.peak, reset when the suite starts where the kernel lets the owner write it (a write resets
// the peak seen through that open file), else the peak since the machine booted.
type machineMeter struct {
	cpu   time.Duration
	peak  *os.File
	scope string
}

func startMeter() (*machineMeter, error) {
	cpu, err := cgroupCPU()
	if err != nil {
		return nil, err
	}
	m := &machineMeter{cpu: cpu, scope: "the journey machine's cgroup, peak since the suite started"}
	m.peak, err = os.OpenFile(cgroupDir+"/memory.peak", os.O_RDWR, 0)
	if err == nil {
		_, err = m.peak.WriteString("reset\n")
	}
	if err != nil {
		if m.peak != nil {
			_ = m.peak.Close()
		}
		m.peak, m.scope = nil, "the journey machine's cgroup, peak since the machine booted"
	}
	return m, nil
}

func (m *machineMeter) read() (harness.Machine, error) {
	cpu, err := cgroupCPU()
	if err != nil {
		return harness.Machine{}, err
	}
	var data []byte
	if m.peak != nil {
		defer func() { _ = m.peak.Close() }()
		data = make([]byte, 64)
		n, err := m.peak.ReadAt(data, 0)
		if n == 0 && err != nil {
			return harness.Machine{}, fmt.Errorf("machine: read %s/memory.peak: %w", cgroupDir, err)
		}
		data = data[:n]
	} else if data, err = os.ReadFile(cgroupDir + "/memory.peak"); err != nil {
		return harness.Machine{}, fmt.Errorf("machine: %w", err)
	}
	peak, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return harness.Machine{}, fmt.Errorf("machine: %s/memory.peak holds %q: %w", cgroupDir, data, err)
	}
	return harness.Machine{CPU: cpu - m.cpu, Peak: peak, Scope: m.scope}, nil
}

// cgroupCPU is the cgroup's whole CPU time so far, cpu.stat's usage_usec.
func cgroupCPU() (time.Duration, error) {
	data, err := os.ReadFile(cgroupDir + "/cpu.stat")
	if err != nil {
		return 0, fmt.Errorf("machine: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if value, ok := strings.CutPrefix(line, "usage_usec "); ok {
			usec, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return 0, fmt.Errorf("machine: %s/cpu.stat usage_usec %q: %w", cgroupDir, value, err)
			}
			return time.Duration(usec) * time.Microsecond, nil
		}
	}
	return 0, fmt.Errorf("machine: %s/cpu.stat has no usage_usec line:\n%s", cgroupDir, data)
}
