package installer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// A plugin of ours installed as {name}@{name} (its own GitHub marketplace) or
// {name}@{name}-dev (the -alpha copy in the pfm-owned directory marketplace
// ~/.local/share/{name}-dev) before every build installed it as
// {name}@professor. Once {name}@professor is installed, pfm install moves each
// legacy id to it: its pluginConfigs carried first (planLegacyCopyRetirement),
// since an uninstall deletes them, then every install record uninstalled with
// its data kept, a project or local one installed as {name}@professor in its
// project first; then each legacy marketplace pfm owns with nothing left
// installed from it removed, the -dev directory deleted, and every legacy key
// left in settings.json dropped (planLegacySettingsSweep).

// legacyDevMarketplace is the pfm-owned directory a plugin's -dev copy
// installed from before {name}@professor.
func legacyDevMarketplace(home, name string) string {
	return filepath.Join(home, ".local", "share", name+"-dev")
}

// legacyMarketplaceOwned reports whether record, the known marketplace of the
// legacy id name@marketplace, is one a plugin of ours installed from: the -dev
// directory pfm wrote, or a GitHub repository of claudePluginOwner.
func legacyMarketplaceOwned(home, name, marketplace string, record claudeMarketplaceRecord) bool {
	switch marketplace {
	case name + "-dev":
		return record.Kind == claudeMarketplaceDirectory &&
			filepath.Clean(record.Path) == legacyDevMarketplace(home, name)
	case name:
		return record.Kind == claudeMarketplaceGitHub && strings.HasPrefix(record.Repo, claudePluginOwner+"/")
	}
	return false
}

// legacyDevDirOwned reports whether the legacy -dev directory of name exists
// and is the one pfm wrote: its .claude-plugin/marketplace.json names
// {name}-dev. A directory holding anything else is never pfm's to delete.
func legacyDevDirOwned(home, name string) (bool, error) {
	path := filepath.Join(legacyDevMarketplace(home, name), ".claude-plugin", "marketplace.json")
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	var manifest devMarketplaceManifest
	return json.Unmarshal(raw, &manifest) == nil && manifest.Name == name+"-dev", nil
}

// removeLegacyClaudePluginCopies moves an installed own target's legacy ids
// to target.ID in the account dir (the steps the comment above names), each
// one a change line. A record it cannot reach, and every command that fails,
// is an error; the steps after it still run where they are safe: a
// marketplace is removed only once nothing is installed from it, and the -dev
// directory only once no marketplace points at it.
func (installer *engine) removeLegacyClaudePluginCopies(
	ctx context.Context,
	run *claudePluginRun,
	target *ClaudePluginTarget,
) error {
	records, err := readClaudePluginInstalls(run.dir)
	if err != nil {
		return fmt.Errorf("legacy ids of %s unchecked: %w", target.ID, err)
	}
	knownPath, known, err := readKnownMarketplaces(run.dir)
	if err != nil {
		return fmt.Errorf("legacy ids of %s unchecked: %w", target.ID, err)
	}
	home := installer.options.Home
	var failures []error
	command := func(message, dir string, argv ...string) error {
		if run.resolveErr != nil {
			err := fmt.Errorf(
				"%s, and the real claude binary that runs it is not resolved: %w",
				message,
				run.resolveErr,
			)
			failures = append(failures, err)
			return err
		}
		argv = append([]string{run.binary, claudePluginCommand}, argv...)
		line := fmt.Sprintf("%s=%s %s", claudeConfigDirEnv, run.dir, shellCommandLine(argv...))
		if dir != "" {
			line = "cd " + shellCommandLine(dir) + " && " + line
		}
		err := installer.change("run "+line+": "+message, func() error {
			options := deps.RunOptions{Env: pluginCommandEnvironment(run.dir), Dir: dir}
			return runClaudePluginCommand(ctx, installer.processRunner(), argv, options)
		})
		if err != nil {
			failures = append(failures, err)
		}
		return err
	}
	gone := map[string]bool{}
	for _, id := range target.LegacyIDs() {
		gone[id] = true
		done := map[[2]string]bool{}
		for _, entry := range records[id] {
			dir, repair := claudePluginScopeDir(id, "uninstall", entry)
			if repair != "" {
				gone[id] = false
				failures = append(failures, fmt.Errorf("legacy %s (scope %s) cannot move to %s: %s",
					id, EscapeControl(entry.Scope), target.ID, repair))
				continue
			}
			key := [2]string{entry.Scope, dir}
			if done[key] {
				continue
			}
			done[key] = true
			reason := "legacy id of " + target.ID
			if entry.Scope != "user" && !slices.ContainsFunc(records[target.ID], func(have claudePluginInstall) bool {
				return have.Scope == entry.Scope && have.ProjectPath == entry.ProjectPath
			}) && command(id+" is installed in this project; "+target.ID+" replaces it", dir,
				"install", target.ID, "--scope", entry.Scope, "-y") != nil {
				gone[id] = false
				continue
			}
			if command(reason, dir, "uninstall", id, "--scope", entry.Scope, "--keep-data") != nil {
				gone[id] = false
			}
		}
	}
	for _, id := range target.LegacyIDs() {
		name, marketplace, _ := strings.Cut(id, "@")
		entry, registered := known[marketplace]
		if !registered || !legacyMarketplaceOwned(home, name, marketplace, entry.Source) {
			continue
		}
		left := false
		for installed, entries := range records {
			_, from, _ := strings.Cut(installed, "@")
			left = left || from == marketplace && len(entries) != 0 && !gone[installed]
		}
		if left {
			continue
		}
		if command("legacy marketplace of "+target.ID+" in "+knownPath+", nothing installed from it",
			"", "marketplace", "remove", marketplace) == nil {
			delete(known, marketplace)
		}
	}
	owned, err := legacyDevDirOwned(home, target.Name)
	if err != nil {
		failures = append(failures, err)
	}
	devDir := legacyDevMarketplace(home, target.Name)
	pointed := false
	for _, entry := range known {
		pointed = pointed ||
			entry.Source.Kind == claudeMarketplaceDirectory && filepath.Clean(entry.Source.Path) == devDir
	}
	if owned && !pointed {
		if err := installer.change("remove "+devDir+": the legacy -dev marketplace of "+target.ID, func() error {
			return os.RemoveAll(devDir)
		}); err != nil {
			failures = append(failures, err)
		}
	}
	var swept []string
	for _, id := range target.LegacyIDs() {
		if gone[id] {
			swept = append(swept, id)
		}
	}
	message, edit, err := planLegacySettingsSweep(run.settingsPath, target, swept)
	if err != nil {
		failures = append(failures, err)
	} else if message != "" {
		if err := installer.change(message, edit); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// planLegacySettingsSweep plans the settings.json edit that drops each of ids,
// legacy ids of target with no install record left, from enabledPlugins and
// pluginConfigs once target.ID is installed, the first legacy pluginConfigs
// entry carried to target.ID when it has none (retireConfigs). It returns ""
// when none is left or the file is absent.
func planLegacySettingsSweep(path string, target *ClaudePluginTarget, ids []string) (string, func() error, error) {
	raw, document, err := readSettingsForEdit(path)
	if err != nil || document == nil {
		return "", nil, err
	}
	enabled, carried, dropped := sweepLegacyIDs(document, target, ids)
	if len(enabled) == 0 && len(dropped) == 0 {
		return "", nil, nil
	}
	var parts []string
	if len(enabled) != 0 {
		parts = append(parts, "drop "+strings.Join(enabled, ", ")+" from enabledPlugins")
	}
	if carried != "" {
		parts = append(parts, "copy the pluginConfigs of "+carried+" to "+target.ID)
	}
	if len(dropped) != 0 {
		parts = append(parts, "drop the pluginConfigs of "+strings.Join(dropped, ", "))
	}
	message := strings.Join(parts, "; ") + " in " + path + ": legacy ids of " + target.ID
	return message, writeSettingsDocument(path, raw, document, func(fresh map[string]any) error {
		sweepLegacyIDs(fresh, target, ids)
		return nil
	}), nil
}

// sweepLegacyIDs applies planLegacySettingsSweep's edit to document and names
// the enabledPlugins keys it dropped, the id whose pluginConfigs it carried and
// the pluginConfigs entries it dropped.
func sweepLegacyIDs(
	document map[string]any,
	target *ClaudePluginTarget,
	ids []string,
) (enabled []string, carried string, dropped []string) {
	keys, _ := document["enabledPlugins"].(map[string]any)
	for _, id := range ids {
		if _, ok := keys[id]; ok {
			delete(keys, id)
			enabled = append(enabled, id)
		}
	}
	carried, dropped = retireConfigs(document, target, ids)
	return enabled, carried, dropped
}

// ClaudePluginLegacyGaps names, for every plugin of ours, each legacy id still
// on this host in the account dir store: enabled or configured in its
// settings.json, recorded in plugins/installed_plugins.json, or the
// marketplace of one, registered in plugins/known_marketplaces.json and owned
// by us; and the pfm-owned -dev directory under home. One gap per plugin
// names them all. A file that cannot be read returns its error, never an
// answer.
func ClaudePluginLegacyGaps(store, home string, targets []ClaudePluginTarget) ([]ClaudePluginGap, error) {
	settings := filepath.Join(store, "settings.json")
	document, err := readClaudeSettingsDocument(settings)
	if err != nil {
		return nil, err
	}
	records, err := readClaudePluginInstalls(store)
	if err != nil {
		return nil, err
	}
	recordPath := filepath.Join(store, "plugins", "installed_plugins.json")
	knownPath, known, err := readKnownMarketplaces(store)
	if err != nil {
		return nil, err
	}
	enabled, _ := document["enabledPlugins"].(map[string]any)
	configs, _ := document["pluginConfigs"].(map[string]any)
	var gaps []ClaudePluginGap
	var failures []error
	for index := range targets {
		target := &targets[index]
		if !target.Own {
			continue
		}
		var left []string
		for _, id := range target.LegacyIDs() {
			if _, ok := enabled[id]; ok {
				left = append(left, id+" in enabledPlugins of "+settings)
			}
			if _, ok := configs[id]; ok {
				left = append(left, id+" in pluginConfigs of "+settings)
			}
			if len(records[id]) != 0 {
				left = append(left, id+" installed in "+recordPath)
			}
			name, marketplace, _ := strings.Cut(id, "@")
			if entry, ok := known[marketplace]; ok && legacyMarketplaceOwned(home, name, marketplace, entry.Source) {
				left = append(left, "marketplace "+marketplace+" in "+knownPath)
			}
		}
		owned, err := legacyDevDirOwned(home, target.Name)
		if err != nil {
			failures = append(failures, fmt.Errorf("plugin %s: %w", target.ID, err))
		}
		if owned {
			left = append(left, legacyDevMarketplace(home, target.Name))
		}
		if len(left) != 0 {
			gaps = append(gaps, ClaudePluginGap{
				Problem: "plugin " + target.Name + " still has legacy copies of " + target.ID + ": " +
					strings.Join(left, "; "),
				Fix: ClaudePluginRepair + " (it moves them to " + target.ID + " once that is installed)",
			})
		}
	}
	return gaps, errors.Join(failures...)
}
