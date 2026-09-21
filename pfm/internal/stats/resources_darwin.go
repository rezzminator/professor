//go:build darwin

package stats

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

func readHostResources(root string, now int64, cpuCount int) (hostResources, error) {
	// An existing override is a Linux-shaped test jail. Honour it verbatim;
	// only the absent native /proc path crosses to Darwin's kernel readers.
	if root != "" {
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			return readLinuxHostResources(root)
		}
	}
	return readDarwinHostResources(now, cpuCount)
}

func readDockerResources(root string) ([]Container, map[string]dockerSample, []string, error) {
	// macOS has no cgroup filesystem. An existing override is still a fixture
	// and must exercise the same reader as Linux.
	if root != "" {
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			return readDocker(root)
		}
	}
	return []Container{}, map[string]dockerSample{}, nil, nil
}

func readDarwinHostResources(now int64, cpuCount int) (hostResources, error) {
	header, err := readDarwinHeader()
	if err != nil {
		return hostResources{}, err
	}
	processes, busy, warnings, err := readDarwinProcesses()
	if err != nil {
		return hostResources{}, err
	}
	// ps reports cumulative process CPU in centiseconds. A wall-clock counter
	// multiplied by the logical CPU count gives the matching host-total unit;
	// the shared delta math can then calculate both host and subtree CPU rates.
	total := uint64(now / 10_000_000)
	if cpuCount > 0 {
		total *= uint64(cpuCount)
	}
	idle := uint64(0)
	if busy <= total {
		idle = total - busy
	}
	return hostResources{total: total, idle: idle, header: header, processes: processes, warnings: warnings}, nil
}

func readDarwinProcesses() (map[int]processSample, uint64, []string, error) {
	result, err := obs.Runner(deps.RealRunner{}).Run(
		context.Background(),
		[]string{deps.Executable("ps"), "-A", "-o", "pid=,ppid=,time=,rss=,comm="},
		deps.RunOptions{},
	)
	if err == nil && result.ExitCode != 0 {
		err = fmt.Errorf("exit status %d", result.ExitCode)
	}
	if err != nil {
		return nil, 0, nil, fmt.Errorf("sample Darwin process counters via ps: %w", err)
	}
	output := result.Stdout
	processes := make(map[int]processSample)
	var busy uint64
	var warnings []string
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) < 5 {
			warnings = append(warnings, fmt.Sprintf("parse Darwin process row: short record %q", line))
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		parent, parentErr := strconv.Atoi(fields[1])
		cpu, cpuErr := parseDarwinCPUTime(fields[2])
		rssKB, rssErr := strconv.ParseUint(fields[3], 10, 64)
		if pidErr != nil || parentErr != nil || cpuErr != nil || rssErr != nil {
			warnings = append(warnings, fmt.Sprintf(
				"parse Darwin process row %q: pid=%v parent=%v cpu=%v rss=%v",
				line, pidErr, parentErr, cpuErr, rssErr,
			))
			continue
		}
		processes[pid] = processSample{
			parent: parent, jiffies: cpu, rss: rssKB * 1024,
			command: strings.Join(fields[4:], " "),
		}
		busy += cpu
	}
	return processes, busy, warnings, nil
}

func parseDarwinCPUTime(value string) (uint64, error) {
	dayCount := uint64(0)
	clock := value
	if days, rest, found := strings.Cut(value, "-"); found {
		parsed, err := strconv.ParseUint(days, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse days: %w", err)
		}
		dayCount = parsed
		clock = rest
	}
	parts := strings.Split(clock, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, fmt.Errorf("unexpected CPU time %q", value)
	}
	seconds, err := strconv.ParseFloat(parts[len(parts)-1], 64)
	if err != nil {
		return 0, fmt.Errorf("parse seconds: %w", err)
	}
	minutes, err := strconv.ParseUint(parts[len(parts)-2], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse minutes: %w", err)
	}
	hours := dayCount * 24
	if len(parts) == 3 {
		parsed, parseErr := strconv.ParseUint(parts[0], 10, 64)
		if parseErr != nil {
			return 0, fmt.Errorf("parse hours: %w", parseErr)
		}
		hours += parsed
	}
	return uint64(math.Round((float64(hours*3600+minutes*60) + seconds) * 100)), nil
}

func readDarwinHeader() (Header, error) {
	total, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return Header{}, fmt.Errorf("read Darwin host memory size: %w", err)
	}
	result, err := obs.Runner(deps.RealRunner{}).
		Run(context.Background(), []string{deps.Executable("vm_stat")}, deps.RunOptions{})
	if err == nil && result.ExitCode != 0 {
		err = fmt.Errorf("exit status %d", result.ExitCode)
	}
	if err != nil {
		return Header{}, fmt.Errorf("sample Darwin host memory via vm_stat: %w", err)
	}
	output := result.Stdout
	pageSize, pages, err := parseDarwinVMStat(string(output))
	if err != nil {
		return Header{}, err
	}
	availablePages := pages["Pages free"] + pages["Pages inactive"] +
		pages["Pages speculative"] + pages["Pages purgeable"]
	available := availablePages * pageSize
	if available > total {
		available = total
	}
	header := Header{MemoryBytes: total, RAMPercent: percent(total-available, total)}
	swap, err := unix.SysctlRaw("vm.swapusage")
	if err != nil {
		return Header{}, fmt.Errorf("read Darwin swap counters: %w", err)
	}
	swapTotal, swapUsed, err := parseDarwinSwap(swap)
	if err != nil {
		return Header{}, err
	}
	if swapTotal > 0 {
		header.SwapPercent = percent(swapUsed, swapTotal)
	}
	return header, nil
}

func parseDarwinVMStat(value string) (uint64, map[string]uint64, error) {
	lines := strings.Split(value, "\n")
	if len(lines) == 0 {
		return 0, nil, fmt.Errorf("parse Darwin vm_stat: empty output")
	}
	marker := "page size of "
	start := strings.Index(lines[0], marker)
	if start < 0 {
		return 0, nil, fmt.Errorf("parse Darwin vm_stat page size: malformed header %q", lines[0])
	}
	sizeField := strings.Fields(lines[0][start+len(marker):])
	if len(sizeField) == 0 {
		return 0, nil, fmt.Errorf("parse Darwin vm_stat page size: missing value")
	}
	pageSize, err := strconv.ParseUint(sizeField[0], 10, 64)
	if err != nil {
		return 0, nil, fmt.Errorf("parse Darwin vm_stat page size %q: %w", sizeField[0], err)
	}
	pages := make(map[string]uint64)
	for _, line := range lines[1:] {
		key, raw, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		count, parseErr := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(raw), "."), 10, 64)
		if parseErr != nil {
			return 0, nil, fmt.Errorf("parse Darwin vm_stat %q: %w", key, parseErr)
		}
		pages[strings.TrimSpace(key)] = count
	}
	return pageSize, pages, nil
}

func parseDarwinSwap(value []byte) (total, used uint64, err error) {
	// Darwin's vm.swapusage sysctl is xsw_usage: total, available, used,
	// page-size, encrypted. The first three fields are uint64 byte counts.
	if len(value) < 24 {
		return 0, 0, fmt.Errorf("parse Darwin swap counters: short xsw_usage record (%d bytes)", len(value))
	}
	return binary.LittleEndian.Uint64(value[:8]), binary.LittleEndian.Uint64(value[16:24]), nil
}
