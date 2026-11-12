//go:build journey

package journey

import (
	"fmt"
	"syscall"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// machineMeter reads what the suite spent on macOS, which keeps no machine-wide CPU or peak memory
// counter: the suite's own process and the children it reaped (its terminals' shells and what they
// waited for), never a daemon launchd or a multiplexer runs.
type machineMeter struct{ cpu time.Duration }

func startMeter() (*machineMeter, error) {
	cpu, _, err := treeUsage()
	if err != nil {
		return nil, err
	}
	return &machineMeter{cpu: cpu}, nil
}

func (m *machineMeter) read() (harness.Machine, error) {
	cpu, peak, err := treeUsage()
	if err != nil {
		return harness.Machine{}, err
	}
	return harness.Machine{
		CPU:   cpu - m.cpu,
		Peak:  peak,
		Scope: "the suite's process tree; macOS has no machine counter",
	}, nil
}

// treeUsage is the CPU time of this process and its reaped children, and the larger of their peak
// resident sizes (bytes on darwin).
func treeUsage() (time.Duration, int64, error) {
	var self, children syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &self); err != nil {
		return 0, 0, fmt.Errorf("machine: getrusage self: %w", err)
	}
	if err := syscall.Getrusage(syscall.RUSAGE_CHILDREN, &children); err != nil {
		return 0, 0, fmt.Errorf("machine: getrusage children: %w", err)
	}
	cpu := time.Duration(0)
	for _, u := range []syscall.Rusage{self, children} {
		cpu += time.Duration(u.Utime.Nano() + u.Stime.Nano())
	}
	return cpu, max(self.Maxrss, children.Maxrss), nil
}
