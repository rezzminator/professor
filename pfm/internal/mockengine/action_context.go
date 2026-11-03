package mockengine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/resolve"
)

func (proc *process) terminalEnvironment(base []string) []string {
	identity := resolve.EnvironmentOf(proc.env)
	return append(
		append([]string(nil), base...),
		herdr.EnvTmux+"="+identity.TMUX,
		herdr.EnvTmuxPane+"="+identity.TMUXPane,
	)
}

func (proc *process) actionTrace() (map[string]any, error) {
	current := map[string]any{obs.FieldBeat: proc.env(obs.EnvBeat), obs.FieldRun: proc.env(obs.EnvRun)}
	path := proc.script.ActionFile
	if path == "" {
		return current, nil
	}
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("mock action_file must be absolute: %s", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read mock action_file %s: %w", path, err)
	}
	var value struct {
		Beat string `json:"beat"`
		Run  string `json:"run"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("decode mock action_file %s: %w", path, err)
	}
	if value.Beat == "" || value.Run == "" {
		return nil, fmt.Errorf("mock action_file %s needs beat and run", path)
	}
	return map[string]any{obs.FieldBeat: value.Beat, obs.FieldRun: value.Run}, nil
}

func (proc *process) actionEnvironment(base []string) ([]string, error) {
	trace, err := proc.actionTrace()
	if err != nil {
		return nil, err
	}
	if proc.script.ActionFile == "" {
		return base, nil
	}
	result := make([]string, 0, len(base)+3)
	for _, item := range base {
		key, _, _ := strings.Cut(item, "=")
		if key != obs.EnvBeat && key != obs.EnvRun && key != obs.EnvBeatFile {
			result = append(result, item)
		}
	}
	return append(
		result,
		obs.EnvBeat+"="+trace[obs.FieldBeat].(string),
		obs.EnvRun+"="+trace[obs.FieldRun].(string),
		obs.EnvBeatFile+"="+proc.script.ActionFile,
	), nil
}
