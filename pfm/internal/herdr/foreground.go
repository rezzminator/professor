package herdr

import (
	"context"
	"encoding/json"
	"fmt"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

const processInfoResultType = "pane_process_info"

// Process is one foreground process of a pane.
type Process struct {
	PID   int      `json:"pid"`
	Name  string   `json:"name"`
	Argv0 string   `json:"argv0"`
	Argv  []string `json:"argv"`
}

// PaneProcesses is a pane's processes as Herdr's pane.process_info names them.
type PaneProcesses struct {
	// Pane is the canonical pane id Herdr answered for.
	Pane string
	// Shell is the pane's shell pid; 0 when Herdr names none.
	Shell int
	// Foreground is nil when Herdr's foreground_processes was missing, null or
	// unreadable; ForegroundErr names the unreadable one. A shell read needs neither.
	Foreground    []Process
	ForegroundErr error
}

// PIDs are the foreground pids; a foreground Herdr did not list is an error,
// never a pane running nothing.
func (processes PaneProcesses) PIDs() ([]int, error) {
	if processes.ForegroundErr != nil {
		return nil, processes.ForegroundErr
	}
	if processes.Foreground == nil {
		return nil, fmt.Errorf(
			"herdr pane.process_info of %q: foreground_processes missing or null", processes.Pane,
		)
	}
	pids := make([]int, 0, len(processes.Foreground))
	for _, process := range processes.Foreground {
		pids = append(pids, process.PID)
	}
	return pids, nil
}

// ParsePaneProcesses decodes a pane.process_info result as Herdr answered it.
func ParsePaneProcesses(body json.RawMessage) (PaneProcesses, error) {
	var decoded struct {
		Type string `json:"type"`
		Info struct {
			Pane       string          `json:"pane_id"`
			Shell      int             `json:"shell_pid"`
			Foreground json.RawMessage `json:"foreground_processes"`
		} `json:"process_info"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return PaneProcesses{}, fmt.Errorf("herdr pane.process_info: decode result %q: %w", body, err)
	}
	if decoded.Type != processInfoResultType {
		return PaneProcesses{}, fmt.Errorf(
			"herdr pane.process_info: result type %q, want %s", decoded.Type, processInfoResultType,
		)
	}
	processes := PaneProcesses{Pane: decoded.Info.Pane, Shell: decoded.Info.Shell}
	if raw := decoded.Info.Foreground; len(raw) > 0 {
		if err := json.Unmarshal(raw, &processes.Foreground); err != nil {
			processes.Foreground, processes.ForegroundErr = nil, fmt.Errorf(
				"herdr pane.process_info of %q: decode foreground_processes %s: %w", processes.Pane, raw, err,
			)
		}
	}
	return processes, nil
}

// ReadPaneProcesses asks socket for pane's pane.process_info and decodes it.
func ReadPaneProcesses(ctx context.Context, socket, pane string) (PaneProcesses, error) {
	body, err := SeatRequest(ctx, socket, "pane.process_info", map[string]string{paneIDKey: pane})
	if err != nil {
		return PaneProcesses{}, err
	}
	return ParsePaneProcesses(body)
}

// EngineForeground reads a pane's processes (ReadPaneProcesses) for both tab
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
