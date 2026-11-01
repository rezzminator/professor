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

func readDockerResources(root string) ([]Container, map[string]dockerSample, bool, []string, error) {
	// macOS has no cgroup filesystem. An existing override is still a fixture
	// and must exercise the same reader as Linux.
	if root != "" {
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			containers, raw, warnings, err := readDocker(root)
			return containers, raw, true, warnings, err
		}
	}
	return []Container{}, map[string]dockerSample{}, false, nil, nil
}

func readDarwinHostResources(now int64, cpuCount int) (hostResources, error) {
	header, warnings, err := readDarwinHeader()
	if err != nil {
		return hostResources{}, err
	}
	processes, busy, processWarnings, err := readDarwinProcesses()
	warnings = append(warnings, processWarnings...)
	if err != nil {
		// No process table is a warning and an empty table, not a failed
		// sample: the header stays, and the zero total is the "no CPU
		// counter" marker the deltas refuse.
		warnings = append(warnings, err.Error())
		return hostResources{header: header, processes: map[int]processSample{}, warnings: warnings}, nil
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
	ps, err := deps.Resolve("ps")
	if err != nil {
		return nil, 0, nil, fmt.Errorf("sample Darwin process counters: resolve ps: %w", err)
	}
	result, err := obs.Runner(deps.RealRunner{}).Run(
		context.Background(),
		[]string{ps, "-A", "-o", "pid=,ppid=,time=,rss=,comm="},
		deps.RunOptions{},
	)
	if err == nil && result.ExitCode != 0 {
		err = fmt.Errorf("exit status %d", result.ExitCode)
	}
	if err != nil {
		return nil, 0, nil, fmt.Errorf("sample Darwin process counters via ps: %w", err)
	}
	processes, busy, warnings := parseDarwinProcessRows(string(result.Stdout))
	return processes, busy, warnings, nil
}

// parseDarwinProcessRows reads `ps -A -o pid=,ppid=,time=,rss=,comm=` output.
func parseDarwinProcessRows(output string) (map[int]processSample, uint64, []string) {
	processes := make(map[int]processSample)
	var busy uint64
	var warnings []string
	for _, line := range strings.Split(output, "\n") {
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
		if pidErr != nil || parentErr != nil {
			// Without its pid and parent a row cannot be placed in any tree.
			warnings = append(warnings, fmt.Sprintf(
				"parse Darwin process row %q: pid=%v parent=%v", line, pidErr, parentErr,
			))
			continue
		}
		if cpuErr != nil || rssErr != nil {
			// The row keeps its place with zero CPU or memory for the field
			// that did not parse; the warning names what is unknown.
			warnings = append(warnings, fmt.Sprintf(
				"parse Darwin process row %q: cpu=%v rss=%v; kept with that reading unknown", line, cpuErr, rssErr,
			))
			if cpuErr != nil {
				cpu = 0
			}
			if rssErr != nil {
				rssKB = 0
			}
		}
		processes[pid] = processSample{
			parent: parent, jiffies: cpu, cpuUnknown: cpuErr != nil, rss: rssKB * 1024,
			command: strings.Join(fields[4:], " "),
		}
		busy += cpu
	}
	return processes, busy, warnings
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

func readDarwinHeader() (Header, []string, error) {
	total, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return Header{}, nil, fmt.Errorf("read Darwin host memory size: %w", err)
	}
	vmStat, err := deps.Resolve("vm_stat")
	if err != nil {
		return Header{}, nil, fmt.Errorf("sample Darwin host memory: resolve vm_stat: %w", err)
	}
	result, err := obs.Runner(deps.RealRunner{}).
		Run(context.Background(), []string{vmStat}, deps.RunOptions{})
	if err == nil && result.ExitCode != 0 {
		err = fmt.Errorf("exit status %d", result.ExitCode)
	}
	if err != nil {
		return Header{}, nil, fmt.Errorf("sample Darwin host memory via vm_stat: %w", err)
	}
	output := result.Stdout
	pageSize, pages, warnings, err := parseDarwinVMStat(string(output))
	if err != nil {
		return Header{}, nil, err
	}
	availablePages := pages["Pages free"] + pages["Pages inactive"] +
		pages["Pages speculative"] + pages["Pages purgeable"]
	available := availablePages * pageSize
	if available > total {
		available = total
	}
	header := Header{MemoryBytes: total, RAMPercent: percent(total-available, total), PSIUnavailable: true}
	// A swap reading that cannot be had leaves SwapPercent at zero with a
	// warning; it never aborts the host sample.
	swap, err := unix.SysctlRaw("vm.swapusage")
	if err != nil {
		header.SwapUnread = true
		return header, append(warnings, fmt.Sprintf("read Darwin swap counters: %v", err)), nil
	}
	swapTotal, swapUsed, err := parseDarwinSwap(swap)
	if err != nil {
		header.SwapUnread = true
		return header, append(warnings, err.Error()), nil
	}
	if swapTotal > 0 {
		header.SwapPercent = percent(swapUsed, swapTotal)
	}
	return header, warnings, nil
}

// parseDarwinVMStat returns the page size and every page counter that parsed;
// a counter that does not parse is skipped with a warning naming it.
func parseDarwinVMStat(value string) (uint64, map[string]uint64, []string, error) {
	lines := strings.Split(value, "\n")
	if len(lines) == 0 {
		return 0, nil, nil, fmt.Errorf("parse Darwin vm_stat: empty output")
	}
	marker := "page size of "
	start := strings.Index(lines[0], marker)
	if start < 0 {
		return 0, nil, nil, fmt.Errorf("parse Darwin vm_stat page size: malformed header %q", lines[0])
	}
	sizeField := strings.Fields(lines[0][start+len(marker):])
	if len(sizeField) == 0 {
		return 0, nil, nil, fmt.Errorf("parse Darwin vm_stat page size: missing value")
	}
	pageSize, err := strconv.ParseUint(sizeField[0], 10, 64)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("parse Darwin vm_stat page size %q: %w", sizeField[0], err)
	}
	pages := make(map[string]uint64)
	var warnings []string
	for _, line := range lines[1:] {
		key, raw, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		count, parseErr := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(raw), "."), 10, 64)
		if parseErr != nil {
			warnings = append(warnings, fmt.Sprintf("parse Darwin vm_stat %q: %v; counter skipped", key, parseErr))
			continue
		}
		pages[strings.TrimSpace(key)] = count
	}
	return pageSize, pages, warnings, nil
}

func parseDarwinSwap(value []byte) (total, used uint64, err error) {
	// Darwin's vm.swapusage sysctl is xsw_usage: total, available, used,
	// page-size, encrypted. The first three fields are uint64 byte counts.
	if len(value) < 24 {
		return 0, 0, fmt.Errorf("parse Darwin swap counters: short xsw_usage record (%d bytes)", len(value))
	}
	return binary.LittleEndian.Uint64(value[:8]), binary.LittleEndian.Uint64(value[16:24]), nil
}
