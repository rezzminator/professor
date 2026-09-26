package gather

import (
	"fmt"
	"sort"
)

// DetectClaudeProcesses maps every live Claude process to a gathered pane.
// Compose uses this fact to decide whether a socket-scoped crumb is still
// trustworthy after its original pane exits.
func DetectClaudeProcesses(
	proc ProcFS,
	panes []ProbePane, binaries ...string,
) ([]ClaudeProcess, error) {
	cmdlines, err := processCmdlines(proc)
	if err != nil {
		return nil, fmt.Errorf("list processes for Claude scan: %w", err)
	}
	return detectClaudeProcessesFrom(cmdlines, proc, panes, binaries...)
}

// detectClaudeProcessesFrom is DetectClaudeProcesses over an already-fetched
// pid->cmdline snapshot (see processCmdlines) — the shape gather.Snapshot
// uses so its parallel detectors share one /proc walk instead of paying for
// one each.
func detectClaudeProcessesFrom(
	cmdlines map[int][]string,
	proc ProcFS,
	panes []ProbePane, binaries ...string,
) ([]ClaudeProcess, error) {
	pids := sortedPIDs(cmdlines)
	paneByPID := panesByPID(panes)
	processes := make([]ClaudeProcess, 0)
	for _, pid := range pids {
		if !IsClaudeCommand(cmdlines[pid], binaries...) {
			continue
		}
		pane, found := paneForProcess(proc, pid, paneByPID)
		if !found {
			continue
		}
		processes = append(processes, ClaudeProcess{
			PID:     pid,
			PanePID: pane.PID,
			Socket:  pane.Socket,
			PaneID:  pane.PaneID,
			TTY:     pane.TTY,
		})
	}
	sort.Slice(processes, func(left, right int) bool {
		if processes[left].Socket != processes[right].Socket {
			return processes[left].Socket < processes[right].Socket
		}
		return processes[left].PID < processes[right].PID
	})
	return processes, nil
}
