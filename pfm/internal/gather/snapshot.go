package gather

import (
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// The macOS process table's one ps snapshot and memory counters, parsed here
// untagged so the Linux fence tests them; snapshot_darwin.go runs the
// commands.

// psRow is one process in a ps snapshot.
type psRow struct {
	counter    ProcessCounter
	rssUnknown bool
	// tty is the controlling terminal's short name, "" without one.
	tty string
}

// parsePSSnapshot reads `ps -A -o pid=,ppid=,time=,rss=,tty=,comm=` output.
// A row without a pid and parent is dropped with a warning; a row whose CPU
// or RSS does not parse is kept with that reading unknown and a warning.
func parsePSSnapshot(output string) (map[int]psRow, []string) {
	rows := make(map[int]psRow)
	var warnings []string
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) < 6 {
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
		tty := fields[4]
		if tty == "??" || tty == "?" {
			tty = ""
		}
		rows[pid] = psRow{
			counter: ProcessCounter{
				Parent: parent, CPUTicks: cpu, CPUUnknown: cpuErr != nil, RSSBytes: rssKB * 1024,
				Command: strings.Join(fields[5:], " "),
			},
			rssUnknown: rssErr != nil,
			tty:        tty,
		}
	}
	return rows, warnings
}

// snapshotTable is the process table a ps snapshot holds. ps always lists at
// least launchd and itself, so no parseable row is a snapshot that could not be
// read, never a table of no processes.
func snapshotTable(output string) (map[int]psRow, error) {
	rows, warnings := parsePSSnapshot(output)
	if len(rows) == 0 {
		return nil, fmt.Errorf("ps snapshot held no parseable process row: %s", strings.Join(warnings, "; "))
	}
	return rows, nil
}

// parseDarwinCPUTime converts ps's [[dd-]hh:]mm:ss.ss CPU time to centiseconds.
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

// darwinMemory composes the host memory from hw.memsize (bytes), vm_stat's
// output and the vm.swapusage record or its read error. Available memory is
// free + inactive + speculative + purgeable pages, capped at the total; a
// swap reading that cannot be had is SwapErr, never a failed read.
func darwinMemory(totalBytes uint64, vmStat string, swap []byte, swapErr error) (Memory, error) {
	pageSize, pages, warnings, err := parseDarwinVMStat(vmStat)
	if err != nil {
		return Memory{}, err
	}
	available := (pages["Pages free"] + pages["Pages inactive"] +
		pages["Pages speculative"] + pages["Pages purgeable"]) * pageSize
	available = min(available, totalBytes)
	memory := Memory{TotalKB: totalBytes / 1024, AvailableKB: available / 1024, Warnings: warnings}
	if swapErr != nil {
		memory.SwapErr = fmt.Errorf("read Darwin swap counters: %w", swapErr)
		return memory, nil
	}
	swapTotal, swapUsed, err := parseDarwinSwap(swap)
	if err != nil {
		memory.SwapErr = err
		return memory, nil
	}
	memory.SwapTotalKB = swapTotal / 1024
	memory.SwapFreeKB = (swapTotal - min(swapUsed, swapTotal)) / 1024
	return memory, nil
}

// parseDarwinVMStat returns the page size and every page counter that parsed;
// a counter that does not parse is skipped with a warning naming it.
func parseDarwinVMStat(value string) (uint64, map[string]uint64, []string, error) {
	lines := strings.Split(value, "\n")
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

// parseDarwinSwap reads Darwin's vm.swapusage sysctl, an xsw_usage record:
// total, available, used, page-size, encrypted. The first three fields are
// uint64 byte counts.
func parseDarwinSwap(value []byte) (total, used uint64, err error) {
	if len(value) < 24 {
		return 0, 0, fmt.Errorf("parse Darwin swap counters: short xsw_usage record (%d bytes)", len(value))
	}
	return binary.LittleEndian.Uint64(value[:8]), binary.LittleEndian.Uint64(value[16:24]), nil
}
