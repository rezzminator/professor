package herdr

import (
	"encoding/json"
	"fmt"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

const processInfoResultType = "pane_process_info"

// EngineForeground interprets pane.process_info for both tab status and kill.
// Herdr 0.9.5 permits an empty array but supplies no successful-enumeration
// marker: without a process identity, engine absence remains unproved.
func EngineForeground(body json.RawMessage, pane string, engine pfmengine.ID) (bool, error) {
	if _, err := pfmengine.Lookup(engine); err != nil {
		return false, fmt.Errorf("herdr foreground engine: %w", err)
	}
	var reply struct {
		Type string `json:"type"`
		Info *struct {
			Pane      string `json:"pane_id"`
			Processes []struct {
				PID   int      `json:"pid"`
				Name  string   `json:"name"`
				Argv0 string   `json:"argv0"`
				Argv  []string `json:"argv"`
			} `json:"foreground_processes"`
		} `json:"process_info"`
	}
	if err := json.Unmarshal(body, &reply); err != nil {
		return false, fmt.Errorf("decode Herdr foreground processes in %s: %w", pane, err)
	}
	if reply.Type != processInfoResultType || reply.Info == nil || reply.Info.Pane != pane {
		return false, fmt.Errorf("read Herdr processes in %s: malformed process_info result", pane)
	}
	if len(reply.Info.Processes) == 0 {
		return false, fmt.Errorf("herdr foreground evidence unavailable in %s; engine exit is unproved", pane)
	}
	present := false
	for _, process := range reply.Info.Processes {
		if process.PID <= 0 || (process.Name == "" && process.Argv0 == "") {
			return false, fmt.Errorf("read Herdr processes in %s: process has no valid pid or identity", pane)
		}
		for _, argv := range [][]string{{process.Name}, {process.Argv0}, process.Argv} {
			present = present || pfmengine.MatchCommand(engine, argv, engine == pfmengine.Claude)
		}
	}
	return present, nil
}
