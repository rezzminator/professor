package gather

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// CPU is the host's cumulative CPU time, in USER_HZ ticks.
type CPU struct {
	Total uint64
	Idle  uint64
}

// ProcessCounter is one process's cumulative counters. CPUTicks is in
// hundredths of a second on both kernels (USER_HZ ticks on Linux, ps
// centiseconds on macOS); CPUUnknown marks a record whose CPU did not parse,
// kept so its memory and tree place survive.
type ProcessCounter struct {
	Parent     int
	CPUTicks   uint64
	CPUUnknown bool
	RSSBytes   uint64
	Command    string
}

// HostCPU reads the host's CPU totals: {root}/stat for a jail root, else the
// kernel's own. macOS keeps no host tick counter a pure-Go reader can reach,
// so there it is errors.ErrUnsupported and the caller derives a total itself.
func HostCPU(root string) (CPU, error) {
	if jail := jailRoot(root); jail != "" {
		return readLinuxCPU(jail)
	}
	return nativeHostCPU()
}

// HostCPUPressure reads the CPU pressure stall avg10 percentage:
// {root}/pressure/cpu for a jail root, else the kernel's own. A kernel without
// PSI returns the read error (fs.ErrNotExist or EOPNOTSUPP), and macOS
// errors.ErrUnsupported. A record with no avg10 field reads 0.
func HostCPUPressure(root string) (float64, error) {
	if jail := jailRoot(root); jail != "" {
		return readLinuxCPUPressure(jail)
	}
	return nativeHostCPUPressure()
}

// ProcessCounters samples every process's counters in one pass: the Linux
// proc tree for a jail root, else the kernel's own table (one ps snapshot on
// macOS). A record that cannot be read or parsed is a warning, never a failed
// sample; a table that cannot be listed is the error.
func ProcessCounters(root string) (map[int]ProcessCounter, []string, error) {
	if jail := jailRoot(root); jail != "" {
		return readLinuxProcessCounters(jail)
	}
	return nativeProcessCounters()
}

func readLinuxCPU(root string) (CPU, error) {
	content, err := os.ReadFile(filepath.Join(root, "stat"))
	if err != nil {
		return CPU{}, fmt.Errorf("read host CPU counters: %w", err)
	}
	line, _, _ := strings.Cut(string(content), "\n")
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return CPU{}, fmt.Errorf("read host CPU counters: malformed first line")
	}
	var cpu CPU
	values := make([]uint64, 0, len(fields)-1)
	for _, field := range fields[1:] {
		value, parseErr := strconv.ParseUint(field, 10, 64)
		if parseErr != nil {
			return CPU{}, fmt.Errorf("parse host CPU counter %q: %w", field, parseErr)
		}
		cpu.Total += value
		values = append(values, value)
	}
	cpu.Idle = values[3]
	if len(values) > 4 {
		cpu.Idle += values[4]
	}
	return cpu, nil
}

func readLinuxCPUPressure(root string) (float64, error) {
	pressure, err := os.ReadFile(filepath.Join(root, "pressure", "cpu"))
	if err != nil {
		return 0, fmt.Errorf("read CPU pressure: %w", err)
	}
	for _, field := range strings.Fields(string(pressure)) {
		if value, found := strings.CutPrefix(field, "avg10="); found {
			avg10, parseErr := strconv.ParseFloat(value, 64)
			if parseErr != nil {
				return 0, fmt.Errorf("parse CPU pressure avg10: %w", parseErr)
			}
			return avg10, nil
		}
	}
	return 0, nil
}

func readLinuxProcessCounters(root string) (map[int]ProcessCounter, []string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, nil, fmt.Errorf("read process root: %w", err)
	}
	processes := make(map[int]ProcessCounter)
	var warnings []string
	for _, entry := range entries {
		pid, parseErr := strconv.Atoi(entry.Name())
		if parseErr != nil || pid <= 0 || !entry.IsDir() {
			continue
		}
		content, readErr := os.ReadFile(filepath.Join(root, entry.Name(), "stat"))
		if readErr != nil {
			if !os.IsNotExist(readErr) {
				warnings = append(warnings, fmt.Sprintf("read process %d stat: %v", pid, readErr))
			}
			continue
		}
		// A record that does not parse keeps the process, with whatever fields
		// did parse and zero ticks, so its memory and its chat membership
		// survive; the warning names the lost CPU reading.
		process, parseErr := parseProcessStat(pid, content)
		if parseErr != nil {
			process.CPUUnknown = true
			warnings = append(warnings, parseErr.Error()+"; process kept with unknown CPU")
		}
		if statm, readErr := os.ReadFile(filepath.Join(root, entry.Name(), "statm")); readErr == nil {
			fields := strings.Fields(string(statm))
			if len(fields) >= 2 {
				pages, parseErr := strconv.ParseUint(fields[1], 10, 64)
				if parseErr == nil {
					process.RSSBytes = pages * uint64(os.Getpagesize())
				}
			}
		} else if !os.IsNotExist(readErr) {
			warnings = append(warnings, fmt.Sprintf("read process %d memory: %v", pid, readErr))
		}
		if command, readErr := os.ReadFile(filepath.Join(root, entry.Name(), "cmdline")); readErr == nil {
			process.Command, _, _ = strings.Cut(string(command), "\x00")
		} else if !os.IsNotExist(readErr) {
			warnings = append(warnings, fmt.Sprintf("read process %d command: %v", pid, readErr))
		}
		processes[pid] = process
	}
	return processes, warnings, nil
}

// parseProcessStat returns the counters the record yields: on an error the
// fields that parsed (the parent, never the ticks) beside the error.
func parseProcessStat(pid int, content []byte) (ProcessCounter, error) {
	closeParen := strings.LastIndex(string(content), ") ")
	if closeParen < 0 {
		return ProcessCounter{}, fmt.Errorf("parse process %d stat: missing command terminator", pid)
	}
	fields := strings.Fields(string(content[closeParen+2:]))
	if len(fields) <= 12 {
		return ProcessCounter{}, fmt.Errorf("parse process %d stat: short record", pid)
	}
	parent, err := strconv.Atoi(fields[1])
	if err != nil {
		return ProcessCounter{}, fmt.Errorf("parse process %d parent: %w", pid, err)
	}
	user, err := strconv.ParseUint(fields[11], 10, 64)
	if err != nil {
		return ProcessCounter{Parent: parent}, fmt.Errorf("parse process %d user ticks: %w", pid, err)
	}
	system, err := strconv.ParseUint(fields[12], 10, 64)
	if err != nil {
		return ProcessCounter{Parent: parent}, fmt.Errorf("parse process %d system ticks: %w", pid, err)
	}
	return ProcessCounter{Parent: parent, CPUTicks: user + system}, nil
}
