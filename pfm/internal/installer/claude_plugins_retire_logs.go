package installer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/claudeplugin"
)

// retirePluginLogDirs removes {root}/{name} for each bare name of ids, root the
// plugin log folder pfm set (retiredPluginLogFolders).
func (installer *engine) retirePluginLogDirs(dirs, ids []string) error {
	folders, err := retiredPluginLogFolders(installer.managedRoot, dirs, ids)
	var failed error
	if err != nil {
		failed = installer.fail(fmt.Errorf("claude plugins: retired plugin logs: %w", err))
	}
	for _, name := range retiredPluginNames(ids) {
		path, ok := folders[name]
		if !ok {
			continue
		}
		if err := installer.change("remove "+path+" (logs of a retired plugin)", func() error {
			return os.RemoveAll(path)
		}); err != nil {
			failed = errors.Join(failed, installer.fail(fmt.Errorf("claude plugins: remove %s: %w", path, err)))
		}
	}
	return failed
}

// retiredPluginLogFolders maps each bare name of ids to its folder under the
// plugin log folder pfm set (the claude env ledger's pluginLogDir in
// managedRoot; none set, none named) while it is there and no id of that name
// ids do not list is still keyed or installed in dirs: that plugin writes to the
// same folder, which is then its own. An error names what could not be read,
// beside every folder that could.
func retiredPluginLogFolders(managedRoot string, dirs, ids []string) (map[string]string, error) {
	ledger, err := readClaudeEnvOwnership(managedRoot)
	if err != nil || ledger.PluginLogDir == "" {
		return nil, err
	}
	folders := map[string]string{}
	var failed error
	for _, name := range retiredPluginNames(ids) {
		path := filepath.Join(ledger.PluginLogDir, name)
		if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			failed = errors.Join(failed, err) // the *PathError names path
			continue
		}
		held, err := pluginNameHeld(dirs, name, ids)
		if err != nil {
			failed = errors.Join(failed, fmt.Errorf("keep or remove %s: %w", path, err))
			continue
		}
		if !held {
			folders[name] = path
		}
	}
	return folders, failed
}

// retiredPluginNames are ids' bare names, sorted, each once.
func retiredPluginNames(ids []string) []string {
	var names []string
	for _, id := range ids {
		names = append(names, retiredPluginName(id))
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// retiredPluginName is id's bare name, the part before its @.
func retiredPluginName(id string) string {
	name, _, _ := strings.Cut(id, "@")
	return name
}

// pluginNameHeld reports whether a settings.json enabledPlugins or an
// installed_plugins.json of dirs still holds an id named name@… that ids do not list.
func pluginNameHeld(dirs []string, name string, ids []string) (bool, error) {
	holds := func(key string) bool {
		return strings.HasPrefix(key, name+"@") && !slices.Contains(ids, key)
	}
	for _, dir := range dirs {
		_, settings, err := readSettingsForEdit(filepath.Join(dir, claudeSettingsName))
		if err != nil {
			return false, err
		}
		enabled, _ := settings["enabledPlugins"].(map[string]any)
		installs, err := claudeplugin.ReadInstalls(dir)
		if err != nil {
			return false, err
		}
		for key := range enabled {
			if holds(key) {
				return true, nil
			}
		}
		for key := range installs {
			if holds(key) {
				return true, nil
			}
		}
	}
	return false, nil
}
