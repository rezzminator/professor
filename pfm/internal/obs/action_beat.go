package obs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const unattributed = "unattributed"

// readActionBeat is the per-action correlation source for long-lived entries.
func readActionBeat(path string) (beat, run string, err error) {
	if !filepath.IsAbs(path) {
		return unattributed, unattributed, fmt.Errorf("action attribution path must be absolute: %s", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return unattributed, unattributed, fmt.Errorf("read action attribution %s: %w", path, err)
	}
	var current struct {
		Beat string `json:"beat"`
		Run  string `json:"run"`
	}
	if err := json.Unmarshal(raw, &current); err != nil {
		return unattributed, unattributed, fmt.Errorf("decode action attribution %s: %w", path, err)
	}
	if current.Beat == "" || current.Run == "" {
		return unattributed, unattributed, fmt.Errorf("action attribution %s requires nonempty beat and run", path)
	}
	return current.Beat, current.Run, nil
}
