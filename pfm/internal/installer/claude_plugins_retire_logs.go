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
	folders, kept, err := retiredPluginLogFolders(installer.managedRoot, dirs, ids)
	var failed error
	if err != nil {
		failed = installer.fail(fmt.Errorf("claude plugins: retired plugin logs: %w", err))
	}
	for _, path := range kept {
		installer.skip("claude plugins: " + path + " kept — it holds files other than plugin logs, under a " +
			"PLUGIN_LOG_DIR pfm did not set; remove the retired plugin's logs there by hand")
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
// writes to the same folder, which is then its own. Under a folder an operator
// set rather than pfm, {root}/{name} counts only while it holds plugin log
// files alone (pluginLogsOnly): that root may hold anything, and such a folder
// holding more is returned in kept. An error names what could not be read,
// beside every folder that could.
func retiredPluginLogFolders(managedRoot string, dirs, ids []string) (
	folders map[string][]string, kept []string, failed error,
) {
	roots, pfmRoot, failed := retiredPluginLogRoots(managedRoot, dirs)
	folders = map[string][]string{}
	for _, name := range retiredPluginNames(ids) {
		var present, others []string
		for _, root := range roots {
			path := filepath.Join(root, name)
			if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
				continue
			} else if err != nil {
				failed = errors.Join(failed, err) // the *PathError names path
				continue
			}
			if root != pfmRoot {
				logs, err := pluginLogsOnly(path)
				if err != nil {
					failed = errors.Join(failed, err)
					continue
				}
				if !logs {
					others = append(others, path)
					continue
				}
			}
			present = append(present, path)
		}
		if len(present) == 0 && len(others) == 0 {
			continue
		}
		held, err := pluginNameHeld(dirs, name, ids)
		if err != nil {
			failed = errors.Join(failed, fmt.Errorf("keep or remove %s: %w",
				strings.Join(append(slices.Clone(present), others...), ", "), err))
			continue
		}
		if held {
			continue // another plugin of that name writes there: the folders are its own
		}
		if len(present) != 0 {
			folders[name] = present
		}
		kept = append(kept, others...)
	}
	return folders, kept, failed
}

// pluginLogsOnly reports whether path is a directory, not a link, holding only
// regular files named as the plugin log library writes them ({level}.jsonl,
// its rotations {level}.jsonl.{n}, other *.jsonl and *.log files) or nothing.
func pluginLogsOnly(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return false, err // the *PathError names path
	}
	if !info.IsDir() {
		return false, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return false, fmt.Errorf("list %s: %w", path, err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.Type().IsRegular() || !strings.Contains(name, ".jsonl") && !strings.HasSuffix(name, ".log") {
			return false, nil
		}
	}
	return true, nil
}

// retiredPluginLogRoots are the plugin log folders, each once: the one pfm set
// (the claude env ledger's pluginLogDir in managedRoot, also returned alone as
// pfmRoot, "" when there is none), then each absolute
// PLUGIN_LOG_DIR the settings.json of dirs holds, an operator's value pfm keeps
// (planClaudeEnv) and the folder plugins then write to. An error names what
// could not be read, beside every folder that could; a settings.json that does
// not read or parse is left to the callers' own read of it (the dir's take,
// retiredPluginTraces), which reports it once.
func retiredPluginLogRoots(managedRoot string, dirs []string) (roots []string, pfmRoot string, failed error) {
	ledger, failed := readClaudeEnvOwnership(managedRoot)
	if failed == nil && ledger.PluginLogDir != "" {
		pfmRoot = filepath.Clean(ledger.PluginLogDir)
		roots = append(roots, pfmRoot)
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
	return roots, pfmRoot, failed
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

// pluginNameHeld reports whether an enabledPlugins of a settings.json or
// settings.local.json, or an installed_plugins.json, of dirs still holds an id
// named name@… that ids do not list. A plugin a project's own settings enable
// runs once installed, and installed_plugins.json records a project-scope
// install too (scope, projectPath).
func pluginNameHeld(dirs []string, name string, ids []string) (bool, error) {
	holds := func(key string) bool {
		return strings.HasPrefix(key, name+"@") && !slices.Contains(ids, key)
	}
	for _, dir := range dirs {
		for _, file := range []string{claudeSettingsName, "settings.local.json"} {
			_, settings, err := readSettingsForEdit(filepath.Join(dir, file))
			if err != nil {
				return false, err
			}
			enabled, _ := settings["enabledPlugins"].(map[string]any)
			for key := range enabled {
				if holds(key) {
					return true, nil
				}
			}
		}
		installs, err := claudeplugin.ReadInstalls(dir)
		if err != nil {
			return false, err
		}
		for key := range installs {
			if holds(key) {
				return true, nil
			}
		}
	}
	return false, nil
}
