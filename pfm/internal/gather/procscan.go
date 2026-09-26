package gather

import (
	"fmt"
	"sort"
)

// processCmdlines reads proc.PIDs() and every pid's cmdline exactly once.
// DetectClaudeProcesses, DetectCodexThreadsInRoots and DetectAgents each
// read every cmdline before this existed — three full /proc walks per gather
// pass, on a box with ~1950 processes, whether or not anyone was watching
// the picker (2026-09-03 real-box measurement: 1741 ticks/30s, ~58% of a
// core, idle). Snapshot's caller fetches this once and hands the same map to
// every detector's ...From twin; each public Detect* function still fetches
// its own copy so a standalone caller (internal/kill, internal/reap) keeps
// working unchanged.
//
// A pid whose cmdline could not be read (already exited, or the read lost a
// race) is simply absent from the result — exactly how every detector
// already treated that case when it read cmdline itself.
func processCmdlines(proc ProcFS) (map[int][]string, error) {
	pids, err := proc.PIDs()
	if err != nil {
		return nil, fmt.Errorf("list processes: %w", err)
	}
	cmdlines := make(map[int][]string, len(pids))
	for _, pid := range pids {
		if cmdline, err := proc.Cmdline(pid); err == nil {
			cmdlines[pid] = cmdline
		}
	}
	return cmdlines, nil
}

// sortedPIDs returns a cmdline snapshot's keys in ascending order, matching
// the deterministic scan order every detector already sorted proc.PIDs()
// into.
func sortedPIDs(cmdlines map[int][]string) []int {
	pids := make([]int, 0, len(cmdlines))
	for pid := range cmdlines {
		pids = append(pids, pid)
	}
	sort.Ints(pids)
	return pids
}
