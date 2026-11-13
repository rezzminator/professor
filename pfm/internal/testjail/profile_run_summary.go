package testjail

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// summary.md of a default run (profile_run.go): rendered from packages.tsv, the last row of each
// process standing for it.

// profileEventExit is the event of a process that recorded its exit (profile.go finish).
const profileEventExit = "exit"

// unitRunResult names a process's last event for summary.md: PASS and FAIL at exit, NEAR-TIMEOUT
// once its watchdog fired, NO-EXIT while it runs or after it was killed before it recorded one.
func unitRunResult(event, exit string) string {
	switch {
	case event == profileEventExit && exit == "0":
		return "PASS"
	case event == profileEventExit:
		return "FAIL"
	case strings.HasSuffix(event, "-imminent"):
		return "NEAR-TIMEOUT"
	default:
		return "NO-EXIT"
	}
}

// renderUnitRun is summary.md of the run named name over its packages.tsv text: the counts, the
// red processes with their diagnosis, then every process by wall, slowest first. A row without
// every column is counted as unreadable, never dropped silently.
func renderUnitRun(name, tsv string) string {
	latest := map[string][]string{}
	var pids []string
	unreadable := 0
	for i, line := range strings.Split(strings.TrimRight(tsv, "\n"), "\n") {
		cells := strings.Split(line, "\t")
		switch {
		case i == 0:
			continue
		case len(cells) != len(unitRunColumns):
			unreadable++
			continue
		}
		if _, seen := latest[cells[1]]; !seen {
			pids = append(pids, cells[1])
		}
		latest[cells[1]] = cells
	}
	rows := make([][]string, 0, len(pids))
	counts := map[string]int{}
	cpu := 0.0
	for _, pid := range pids {
		row := latest[pid]
		counts[unitRunResult(row[2], row[3])]++
		cpu += seconds(row[5])
		rows = append(rows, row)
	}
	slices.SortStableFunc(rows, func(a, b []string) int { return compareDesc(seconds(a[4]), seconds(b[4])) })
	var b strings.Builder
	b.WriteString("# Unit run profile\n\n")
	fmt.Fprintf(
		&b,
		"- run: %s · package processes: %d · passed %d · failed %d · near timeout %d · without an exit %d\n",
		name,
		len(rows),
		counts["PASS"],
		counts["FAIL"],
		counts["NEAR-TIMEOUT"],
		counts["NO-EXIT"],
	)
	slowest := "none"
	if len(rows) > 0 {
		slowest = fmt.Sprintf("%s s %s", rows[0][4], rows[0][0])
	}
	fmt.Fprintf(&b, "- CPU: %.1f s over every process · slowest: %s\n", cpu, slowest)
	b.WriteString("- per process: {package}.{pid}/summary.json · every event: packages.tsv\n")
	if unreadable > 0 {
		fmt.Fprintf(&b, "- unreadable rows in packages.tsv: %d\n", unreadable)
	}
	b.WriteString("\n## Red\n\n| result | package | exit | diagnosis |\n|---|---|---|---|\n")
	red := 0
	for _, row := range rows {
		if result := unitRunResult(row[2], row[3]); result != "PASS" {
			fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", result, row[0], row[3], row[8])
			red++
		}
	}
	if red == 0 {
		b.WriteString("| none | | | |\n")
	}
	b.WriteString("\n## Packages by wall\n\n| wall s | cpu s | tests | result | package |\n|---|---|---|---|---|\n")
	for _, row := range rows {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", row[4], row[5], row[7], unitRunResult(row[2], row[3]), row[0])
	}
	return b.String()
}

// seconds reads a packages.tsv seconds cell; "-" or anything unparsable is 0.
func seconds(cell string) float64 {
	v, err := strconv.ParseFloat(cell, 64)
	if err != nil {
		return 0
	}
	return v
}

func compareDesc(a, b float64) int {
	switch {
	case a > b:
		return -1
	case a < b:
		return 1
	}
	return 0
}
