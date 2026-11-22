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

// retirePluginLogDirs removes {root}/{name} for each bare name of ids and each
// plugin log folder (retiredPluginLogFolders).
func (installer *engine) retirePluginLogDirs(dirs, ids []string) error {
	folders, err := retiredPluginLogFolders(installer.managedRoot, dirs, ids)
	var failed error
	if err != nil {
		failed = installer.fail(fmt.Errorf("claude plugins: retired plugin logs: %w", err))
	}
	for _, name := range retiredPluginNames(ids) {
		for _, path := range folders[name] {
			if err := installer.change("remove "+path+" (logs of a retired plugin)", func() error {
				return os.RemoveAll(path)
			}); err != nil {
				failed = errors.Join(failed, installer.fail(fmt.Errorf("claude plugins: remove %s: %w", path, err)))
			}
		}
	}
	return failed
}

// retiredPluginLogFolders maps each bare name of ids to its folders under the
// plugin log folders (retiredPluginLogRoots) while they are there and no id of
// that name ids do not list is still keyed or installed in dirs: that plugin
// writes to the same folder, which is then its own. An error names what could
// not be read, beside every folder that could.
func retiredPluginLogFolders(managedRoot string, dirs, ids []string) (map[string][]string, error) {
	roots, failed := retiredPluginLogRoots(managedRoot, dirs)
	folders := map[string][]string{}
	for _, name := range retiredPluginNames(ids) {
		var present []string
		for _, root := range roots {
			path := filepath.Join(root, name)
			if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
				continue
			} else if err != nil {
				failed = errors.Join(failed, err) // the *PathError names path
				continue
			}
			present = append(present, path)
		}
		if len(present) == 0 {
			continue
		}
		held, err := pluginNameHeld(dirs, name, ids)
		if err != nil {
			failed = errors.Join(failed, fmt.Errorf("keep or remove %s: %w", strings.Join(present, ", "), err))
			continue
		}
		if !held {
			folders[name] = present
		}
	}
	return folders, failed
}

// retiredPluginLogRoots are the plugin log folders, each once: the one pfm set
// (the claude env ledger's pluginLogDir in managedRoot), then each absolute
// PLUGIN_LOG_DIR the settings.json of dirs holds, an operator's value pfm keeps
// (planClaudeEnv) and the folder plugins then write to. An error names what
// could not be read, beside every folder that could; a settings.json that does
// not read or parse is left to the callers' own read of it (the dir's take,
// retiredPluginTraces), which reports it once.
func retiredPluginLogRoots(managedRoot string, dirs []string) ([]string, error) {
	var roots []string
	ledger, failed := readClaudeEnvOwnership(managedRoot)
	if failed == nil && ledger.PluginLogDir != "" {
		roots = append(roots, ledger.PluginLogDir)
	}
	for _, setting := range ReadPluginLogEnv(dirs) {
		if setting.Err != nil {
			if _, _, err := readSettingsForEdit(setting.Settings); err == nil {
				failed = errors.Join(failed, setting.Err)
			}
			continue
		}
		value := filepath.Clean(setting.Value)
		if !setting.Absent && filepath.IsAbs(value) && !slices.Contains(roots, value) {
			roots = append(roots, value)
		}
	}
	return roots, failed
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
