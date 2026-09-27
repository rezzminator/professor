//go:build e2e

package e2e

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestClaudePluginInstallFixture is run by the jailed Claude stub for plugin
// commands. It writes the same two surfaces the installer must journal.
func TestClaudePluginInstallFixture(t *testing.T) {
	if os.Getenv("PFM_E2E_CLAUDE_PLUGIN") != "1" {
		t.Skip("Claude plugin fixture subprocess only")
	}
	home := os.Getenv(e2eHomeEnv)
	// testjail.Run clears ambient CLAUDE_CONFIG_DIR before a fixture test.
	dir := os.Getenv("PFM_E2E_PLUGIN_CONFIG_DIR")
	if home == "" || dir == "" {
		t.Fatal("plugin fixture requires a private home and config dir")
	}
	relative, err := filepath.Rel(home, dir)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		t.Fatalf("plugin fixture escaped its private home: %s", dir)
	}
	separator := -1
	for index, arg := range os.Args {
		if arg == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		t.Fatal("plugin fixture missing argv")
	}
	args := os.Args[separator+1:]
	pluginsDir := filepath.Join(dir, "plugins")
	if len(args) >= 4 && args[0] == "plugin" && args[1] == "marketplace" && args[2] == "add" {
		if err := os.MkdirAll(pluginsDir, 0o700); err != nil {
			t.Fatal(err)
		}
		return
	}
	if len(args) < 3 || args[0] != "plugin" || args[1] != "install" {
		t.Fatalf("unexpected plugin command: %v", args)
	}
	id := args[2]
	settingsPath := filepath.Join(dir, "settings.json")
	raw, err := os.ReadFile(settingsPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	settings := map[string]any{}
	if err == nil {
		if err := json.Unmarshal(raw, &settings); err != nil {
			t.Fatal(err)
		}
	}
	enabled, _ := settings["enabledPlugins"].(map[string]any)
	if enabled == nil {
		enabled = map[string]any{}
	}
	enabled[id] = true
	settings["enabledPlugins"] = enabled
	updated, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, append(updated, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	installPath := filepath.Join(pluginsDir, "cache", id)
	if err := os.MkdirAll(installPath, 0o700); err != nil {
		t.Fatal(err)
	}
	recordPath := filepath.Join(pluginsDir, "installed_plugins.json")
	record := struct {
		Plugins map[string][]map[string]string `json:"plugins"`
	}{Plugins: map[string][]map[string]string{}}
	if raw, err := os.ReadFile(recordPath); err == nil {
		if err := json.Unmarshal(raw, &record); err != nil {
			t.Fatal(err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if record.Plugins == nil {
		record.Plugins = map[string][]map[string]string{}
	}
	record.Plugins[id] = []map[string]string{{"installPath": installPath}}
	updated, err = json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recordPath, append(updated, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}
