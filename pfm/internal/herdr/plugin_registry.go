package herdr

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PluginRegistryPath is Herdr's plugin registry under home: plugins.json, a
// JSON array of entries carrying plugin_id.
func PluginRegistryPath(home string) string {
	return filepath.Join(home, ".config", "herdr", "plugins.json")
}

// PluginLinked reports whether Herdr's plugin registry under home lists id. A
// missing registry is no plugin; an unreadable or malformed one is an error.
func PluginLinked(home, id string) (bool, error) {
	path := PluginRegistryPath(home)
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("herdr: read %s: %w", path, err)
	}
	var entries []struct {
		PluginID string `json:"plugin_id"`
	}
	if err := json.Unmarshal(content, &entries); err != nil {
		return false, fmt.Errorf("herdr: parse %s: %w", path, err)
	}
	for _, entry := range entries {
		if entry.PluginID == id {
			return true, nil
		}
	}
	return false, nil
}

// serverNotRunning are the texts a herdr command prints when no server is up.
var serverNotRunning = []string{
	"not running", "no server", "no running", "connection refused", "could not connect",
	"couldn't connect", "failed to connect", "server_not_running",
}

// ServerNotRunning reports whether text, a herdr command's output, says no
// server is up; "no such file" says so only about a socket (`dial unix
// {path}.sock: … no such file`), never about another missing file.
func ServerNotRunning(text string) bool {
	lower := strings.ToLower(text)
	for _, marker := range serverNotRunning {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return strings.Contains(lower, "no such file") && strings.Contains(lower, "sock")
}
