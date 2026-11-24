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
	folders, kept, err := retiredPluginLogFolders(installer.options.Home, installer.managedRoot, dirs, ids)
	var failed error
	if err != nil {
		failed = installer.fail(fmt.Errorf("claude plugins: retired plugin logs: %w", err))
	}
	for _, row := range kept {
		installer.skip("claude plugins: " + row)
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
// writes to the same folder, which is then its own. Under any folder but pfm's
// own default (pluginLogDirDefault), an operator's choice in pfm.config or in
// settings alike, {root}/{name} counts only while it holds plugin log files
// alone (pluginLogsOnly): that root may hold anything, and such a folder
// holding more is returned in kept, as "{path} kept — {why}". An error names
// what could not be read, beside every folder that could; a record
// pluginNameHeld cannot read keeps that name's folders, named in the error,
// or in kept when it is a settings.local.json (retiredPluginLocalUnread): the
// folder stays either way, since another plugin of that name may write there.
func retiredPluginLogFolders(home, managedRoot string, dirs, ids []string) (
	folders map[string][]string, kept []string, failed error,
) {
	roots, pfmRoot, failed := retiredPluginLogRoots(home, managedRoot, dirs)
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
		held, unread, err := pluginNameHeld(dirs, name, ids)
		if err != nil {
			failed = errors.Join(failed, fmt.Errorf("%s kept: %w",
				strings.Join(append(slices.Clone(present), others...), ", "), err))
			continue
		}
		if held {
			continue // another plugin of that name writes there: the folders are its own
		}
		if len(unread) != 0 {
			for _, path := range slices.Concat(present, others) {
				kept = append(kept, path+" kept — "+strings.Join(unread, "; "))
			}
			continue
		}
		if len(present) != 0 {
			folders[name] = present
		}
		for _, path := range others {
			kept = append(kept, path+" kept — it holds files other than plugin logs, under a PLUGIN_LOG_DIR "+
				"other than pfm's own default; remove the retired plugin's logs there by hand")
		}
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
// (the claude env ledger's pluginLogDir in managedRoot; returned alone as
// pfmRoot only while it is pfm's own default under home, pluginLogDirDefault,
// since pfm.config's claude.env may name any folder), then each absolute
// PLUGIN_LOG_DIR the settings.json of dirs holds, an operator's value pfm keeps
// (planClaudeEnv) and the folder plugins then write to. An error names what
// could not be read, beside every folder that could; a settings.json that does
// not read or parse, or that the take reads as absent (a dangling link), is
// left to the callers' own read of it (the dir's take, retiredPluginTraces),
// which reports it once.
func retiredPluginLogRoots(home, managedRoot string, dirs []string) (roots []string, pfmRoot string, failed error) {
	ledger, failed := readClaudeEnvOwnership(managedRoot)
	if failed == nil && ledger.PluginLogDir != "" {
		root := filepath.Clean(ledger.PluginLogDir)
		roots = append(roots, root)
		if root == pluginLogDirDefault(home) {
			pfmRoot = root
		}
	}
	for _, setting := range ReadPluginLogEnv(dirs) {
		if setting.Err != nil {
			if _, document, err := readSettingsForEdit(setting.Settings); err == nil && document != nil {
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
// install too (scope, projectPath). A record it cannot read is an error naming
// that file as what blocks the check; a settings.local.json is that text in
// unread instead (retiredPluginLocalUnread), while held stays an answer only
// once another record holds the name.
func pluginNameHeld(dirs []string, name string, ids []string) (held bool, unread []string, failed error) {
	holds := func(key string) bool {
		return strings.HasPrefix(key, name+"@") && !slices.Contains(ids, key)
	}
	blocks := func(path string, err error) error {
		return fmt.Errorf("%s blocks the check that no other plugin named %s still writes there: %w", path, name, err)
	}
	for _, dir := range dirs {
		for _, file := range []string{claudeSettingsName, claudeLocalSettingsName} {
			path := filepath.Join(dir, file)
			_, settings, err := readSettingsForEdit(path)
			if err != nil && file == claudeLocalSettingsName {
				unread = append(unread, blocks(path, err).Error())
				continue
			}
			if err != nil {
				return false, nil, blocks(path, err)
			}
			enabled, _ := settings[claudeEnabledPlugins].(map[string]any)
			for key := range enabled {
				if holds(key) {
					return true, nil, nil
				}
			}
		}
		installs, err := claudeplugin.ReadInstalls(dir)
		if err != nil {
			return false, nil, blocks(filepath.Join(dir, claudePlugins, "installed_plugins.json"), err)
		}
		for key := range installs {
			if holds(key) {
				return true, nil, nil
			}
		}
	}
	return false, unread, nil
}
