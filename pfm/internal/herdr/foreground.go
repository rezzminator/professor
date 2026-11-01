package herdr

import (
	"fmt"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

const processInfoResultType = "pane_process_info"

// EngineForeground reads a pane's processes (Keep.Processes) for both tab
// status and kill. Herdr 0.9.5 permits an empty array but supplies no
// successful-enumeration marker: without a process identity, engine absence
// remains unproved.
func EngineForeground(processes PaneProcesses, pane string, engine pfmengine.ID) (bool, error) {
	if _, err := pfmengine.Lookup(engine); err != nil {
		return false, fmt.Errorf("herdr foreground engine: %w", err)
	}
	if processes.Pane != pane {
		return false, fmt.Errorf(
			"read Herdr processes in %s: malformed process_info result for pane %q",
			pane,
			processes.Pane,
		)
	}
	if processes.ForegroundErr != nil {
		return false, fmt.Errorf("decode Herdr foreground processes in %s: %w", pane, processes.ForegroundErr)
	}
	if len(processes.Foreground) == 0 {
		return false, fmt.Errorf("herdr foreground evidence unavailable in %s; engine exit is unproved", pane)
	}
	// An unidentifiable process proves nothing either way: a matched engine
	// beside it is still present, but without a match absence is unproved.
	present, unidentified := false, false
	for _, process := range processes.Foreground {
		if process.PID <= 0 || (process.Name == "" && process.Argv0 == "") {
			unidentified = true
			continue
		}
		for _, argv := range [][]string{{process.Name}, {process.Argv0}, process.Argv} {
			present = present || pfmengine.MatchCommand(engine, argv, engine == pfmengine.Claude)
		}
	}
	if !present && unidentified {
		return false, fmt.Errorf("read Herdr processes in %s: process has no valid pid or identity", pane)
	}
	return present, nil
}
