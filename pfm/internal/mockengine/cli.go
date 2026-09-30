package mockengine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func answerCLI(proc *process) (int, bool) {
	args := proc.args
	if len(args) == 0 {
		return 0, false
	}
	if (proc.engine == engineClaude || proc.engine == engineCodex) && args[0] == "doctor" {
		fmt.Fprintf(proc.stdout, "mock-engine: %s doctor — fixture engine, nothing checked\n", proc.engine)
		return 0, true
	}
	if proc.engine != engineClaude || args[0] != "plugin" {
		return 0, false
	}
	switch {
	case len(args) == 4 && args[1] == "marketplace" && args[2] == "add":
		fmt.Fprintf(proc.stdout, "mock-engine: claude plugin marketplace add %s — fixture, nothing fetched\n", args[3])
		return 0, true
	case (len(args) == 3 || len(args) == 4 && args[3] == "-y") && args[1] == "install":
		configDir := claudeConfigDir(proc)
		if err := recordClaudePlugin(configDir, args[2]); err != nil {
			fmt.Fprintf(proc.stderr, "mock-engine: %v\n", err)
			return ExitUsage, true
		}
		fmt.Fprintf(
			proc.stdout,
			"mock-engine: claude plugin install %s — fixture, recorded installed and enabled in %s\n",
			args[2],
			configDir,
		)
		return 0, true
	default:
		fmt.Fprintf(proc.stderr, "mock-engine: claude %s is not scripted\n", strings.Join(args, " "))
		return ExitUnpinned, true
	}
}

func claudeConfigDir(proc *process) string {
	if dir := proc.env("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(proc.env("HOME"), ".claude")
}

func readPluginDocument(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	if document == nil {
		return nil, fmt.Errorf("decode %s: expected object", path)
	}
	return document, nil
}

func recordClaudePlugin(configDir, id string) error {
	settingsPath := filepath.Join(configDir, "settings.json")
	installedPath := filepath.Join(configDir, "plugins", "installed_plugins.json")
	settings, err := readPluginDocument(settingsPath)
	if err != nil {
		return err
	}
	installed, err := readPluginDocument(installedPath)
	if err != nil {
		return err
	}
	enabled, _ := settings["enabledPlugins"].(map[string]any)
	if enabled == nil {
		enabled = map[string]any{}
	}
	enabled[id] = true
	settings["enabledPlugins"] = enabled
	plugins, _ := installed["plugins"].(map[string]any)
	if plugins == nil {
		plugins = map[string]any{}
	}
	cachePath := filepath.Join(configDir, "plugins", "cache", id)
	plugins[id] = []map[string]string{{"installPath": cachePath}}
	installed["plugins"] = plugins
	settingsData, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", settingsPath, err)
	}
	installedData, err := json.MarshalIndent(installed, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", installedPath, err)
	}
	if err := os.MkdirAll(cachePath, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", cachePath, err)
	}
	if err := os.WriteFile(settingsPath, append(settingsData, '\n'), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", settingsPath, err)
	}
	if err := os.WriteFile(installedPath, append(installedData, '\n'), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", installedPath, err)
	}
	return nil
}
