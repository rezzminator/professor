package mockengine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tailscale/hujson"
)

func openCodeConfigRoot(proc *process) string {
	if root := proc.env("OPENCODE_CONFIG_DIR"); root != "" {
		return root
	}
	root := proc.env("XDG_CONFIG_HOME")
	if root == "" {
		root = filepath.Join(proc.env("HOME"), ".config")
	}
	return filepath.Join(root, "opencode")
}

func openCodeConfig(proc *process) (map[string]any, error) {
	config := map[string]any{}
	for _, directory := range []string{openCodeConfigRoot(proc), proc.cwd, filepath.Join(proc.cwd, ".opencode")} {
		for _, name := range []string{"opencode.jsonc", "opencode.json"} {
			path := filepath.Join(directory, name)
			raw, err := os.ReadFile(path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("read OpenCode config %s: %w", path, err)
			}
			if err := mergeOpenCodeConfig(config, raw, true); err != nil {
				return nil, fmt.Errorf("decode OpenCode config %s: %w", path, err)
			}
		}
	}
	if path := proc.env("OPENCODE_CONFIG"); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read OpenCode config %s: %w", path, err)
		}
		if err := mergeOpenCodeConfig(config, raw, true); err != nil {
			return nil, fmt.Errorf("decode OpenCode config %s: %w", path, err)
		}
	}
	if raw := proc.env("OPENCODE_CONFIG_CONTENT"); raw != "" {
		if err := mergeOpenCodeConfig(config, []byte(raw), false); err != nil {
			return nil, fmt.Errorf("decode OpenCode plugin config: %w", err)
		}
	}
	return config, nil
}

func mergeOpenCodeConfig(config map[string]any, raw []byte, jsonc bool) error {
	if jsonc {
		normalized, err := hujson.Standardize(raw)
		if err != nil {
			return err
		}
		raw = normalized
	}
	var layer map[string]any
	if err := json.Unmarshal(raw, &layer); err != nil {
		return err
	}
	overlayOpenCodeConfig(config, layer)
	return nil
}

// JSON decoding replaces nested maps; OpenCode's config layers overlay their entries.
func overlayOpenCodeConfig(config, layer map[string]any) {
	for key, value := range layer {
		current, currentMap := config[key].(map[string]any)
		incoming, incomingMap := value.(map[string]any)
		if currentMap && incomingMap {
			overlayOpenCodeConfig(current, incoming)
			continue
		}
		if key == openCodePluginKey || key == "instructions" {
			if before, ok := config[key].([]any); ok {
				if after, ok := value.([]any); ok {
					config[key] = append(before, after...)
					continue
				}
			}
		}
		config[key] = value
	}
}
