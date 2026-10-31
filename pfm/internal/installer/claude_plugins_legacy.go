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

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// A plugin of ours installed as {name}@{name} (its own GitHub marketplace) or
// {name}@{name}-dev (the -alpha copy in the pfm-owned directory marketplace
// ~/.local/share/{name}-dev) before every build installed it as
// {name}@professor, or under a former name (FormerNames). Once
// {name}@professor is installed, pfm install moves each legacy id to it: its pluginConfigs carried first (planLegacyCopyRetirement),
// since an uninstall deletes them, then every install record uninstalled with
// its data kept, a project or local one installed as {name}@professor in its
// project first; then each legacy marketplace pfm owns with nothing left
// installed from it removed, the -dev directory deleted, and every legacy key
// left in settings.json dropped (planLegacySettingsSweep); a former name's
// copy in the local professor marketplace goes once {former}@professor is.

// formerProfessorManifest is the local professor marketplace's manifest, and
// formerProfessorEntry reports whether it lists former, a former name of a
// plugin of ours. A manifest that is absent, does not parse or names another
// marketplace lists nothing pfm may drop.
func formerProfessorManifest(home, former string) (string, devMarketplaceManifest, bool, error) {
	path := filepath.Join(professorDevMarketplace(home), ".claude-plugin", "marketplace.json")
	manifest, _, err := devMarketplaceCurrent(path, former)
	listed := slices.ContainsFunc(manifest.Plugins, func(entry devMarketplaceEntry) bool {
		return entry.Name == former
	})
	return path, manifest, listed, err
}

// formerProfessorCopies names what is left of former, a former name of a
// plugin of ours, in the local professor marketplace: its manifest entry and
// its copy under plugins/.
func formerProfessorCopies(home, former string) ([]string, error) {
	path, _, listed, err := formerProfessorManifest(home, former)
	if err != nil {
		return nil, err
	}
	var left []string
	if listed {
		left = append(left, former+" in "+path)
	}
	copyDir := filepath.Join(professorDevMarketplace(home), "plugins", former)
	if _, err := os.Lstat(copyDir); err == nil {
		left = append(left, copyDir)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return left, fmt.Errorf("inspect %s: %w", copyDir, err)
	}
	return left, nil
}

// removeFormerProfessorCopy drops former, a former name of target's plugin
// with no install record left, from the local professor marketplace: its
// manifest entry, then its copy under plugins/. Both stay while wait, asked
// with the copy, reports a live chat that may run from it.
func (installer *engine) removeFormerProfessorCopy(home, former, targetID string, wait func(string) bool) error {
	path, manifest, listed, err := formerProfessorManifest(home, former)
	if err != nil {
		return err
	}
	copyDir := filepath.Join(professorDevMarketplace(home), "plugins", former)
	_, err = os.Lstat(copyDir)
	copied := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("inspect %s: %w", copyDir, err)
	}
	if !listed && !copied || wait(copyDir) {
		return nil
	}
	reason := ": the former name of " + targetID
	if listed {
		manifest.Plugins = slices.DeleteFunc(manifest.Plugins, func(entry devMarketplaceEntry) bool {
			return entry.Name == former
		})
		if err := installer.writeDevMarketplaceManifest(
			"drop "+former+" from "+path+reason,
			path,
			manifest,
		); err != nil {
			return err
		}
	}
	if !copied {
		return nil
	}
	return installer.change("remove "+copyDir+reason, func() error { return os.RemoveAll(copyDir) })
}

// pluginChatsLive asks the live-chat deferral door of every account whose chats
// may run a plugin copy (the roster and dir, the plugin step's own) and returns
// the first reason to wait, "" when none, with the release of every guard it
// took: those guards are held until the copies are gone.
func (installer *engine) pluginChatsLive(dir string) (string, func() error, error) {
	dirs := append([]string{dir}, installer.options.RosterConfigDirs...)
	for _, account := range installer.options.ClaudeAccounts {
		dirs = append(dirs, account.ConfigDir)
	}
	var releases []func() error
	release := func() error {
		var errs []error
		for _, release := range releases {
			errs = append(errs, release())
		}
		return errors.Join(errs...)
	}
	wait := ""
	seen := map[string]bool{}
	for _, dir := range dirs {
		physical := paths.PhysicalPath(dir)
		if dir == "" || seen[physical] {
			continue
		}
		seen[physical] = true
		if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return "", release, fmt.Errorf("inspect account %s: %w", dir, err)
		}
		reason, free, err := installer.liveChatDeferral(dir)
		releases = append(releases, free)
		if err != nil {
			return "", release, fmt.Errorf("read live chats in %s: %w", dir, err)
		}
		if wait == "" {
			wait = reason
		}
	}
	return wait, release, nil
}

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
// one a change line. Only a failed user-scope install of target.ID, which
// leaves the user without it, is an error. A record it cannot reach, and every
// other step that fails, leaves that legacy copy in place as one warn line,
// doctor's legacy gap naming it until a later run moves it; the steps after it
// still run where they are safe: a marketplace is removed only once nothing is
// installed from it, and the -dev directory only once no marketplace points
// at it. A -dev directory or former name's copy a live chat may run its hooks
// from stays, through the live-chat deferral door, until a run with none.
func (installer *engine) removeLegacyClaudePluginCopies(
	ctx context.Context,
	run *claudePluginRun,
	target *ClaudePluginTarget,
) (returnErr error) {
	records, err := readClaudePluginInstalls(run.dir)
	if err != nil {
		return fmt.Errorf("legacy ids of %s unchecked: %w", target.ID, err)
	}
	knownPath, known, err := readKnownMarketplaces(run.dir)
	if err != nil {
		return fmt.Errorf("legacy ids of %s unchecked: %w", target.ID, err)
	}
	home := installer.options.Home
	var failures, kept []error
	command := func(message, dir string, argv ...string) error {
		if run.resolveErr != nil {
			return fmt.Errorf(
				"%s, and the real claude binary that runs it is not resolved: %w",
				message,
				run.resolveErr,
			)
		}
		argv = append([]string{run.binary, claudePluginCommand}, argv...)
		line := fmt.Sprintf("%s=%s %s", claudeConfigDirEnv, run.dir, shellCommandLine(argv...))
		if dir != "" {
			line = "cd " + shellCommandLine(dir) + " && " + line
		}
		return installer.change("run "+line+": "+message, func() error {
			options := deps.RunOptions{Env: pluginCommandEnvironment(run.dir), Dir: dir}
			return runClaudePluginCommand(ctx, installer.processRunner(), argv, options)
		})
	}
	keep := func(err error) { kept = append(kept, fmt.Errorf("%w — %s to retry", err, ClaudePluginRepair)) }
	// The door is asked once, before the first copy a chat may run from goes.
	asked, waitReason, waitErr := false, "", error(nil)
	release := func() error { return nil }
	defer func() { returnErr = errors.Join(returnErr, release()) }()
	wait := func(path string) bool {
		if !asked {
			asked = true
			waitReason, release, waitErr = installer.pluginChatsLive(run.dir)
			if waitErr != nil {
				keep(waitErr)
			}
		}
		if waitReason != "" {
			installer.skip("keep " + path + ": " + waitReason + " — pfm doctor names the fix")
		}
		return waitReason != "" || waitErr != nil
	}
	gone := map[string]bool{}
	for _, id := range target.LegacyIDs() {
		gone[id] = true
		done := map[[2]string]bool{}
		for _, entry := range records[id] {
			dir, repair := claudePluginScopeDir(id, "uninstall", entry)
			if repair != "" {
				gone[id] = false
				kept = append(kept, fmt.Errorf("legacy %s (scope %s) cannot move to %s: %s",
					id, EscapeControl(entry.Scope), target.ID, repair))
				continue
			}
			key := [2]string{entry.Scope, dir}
			if done[key] {
				continue
			}
			done[key] = true
			reason := "legacy id of " + target.ID
			// A target recorded only in a project is no user-scope install: the user-scope
			// legacy id goes only once the target is installed there too. A target this
			// pass installed (run.missing) is at user scope already, or previewed so.
			userScope, where := entry.Scope == "user", "in this project"
			if userScope {
				where = "for this user"
			}
			if (!userScope || !slices.Contains(run.missing, target.ID)) &&
				!slices.ContainsFunc(records[target.ID], func(have claudePluginInstall) bool {
					return have.Scope == entry.Scope && have.ProjectPath == entry.ProjectPath
				}) {
				if err := command(id+" is installed "+where+"; "+target.ID+" replaces it", dir,
					"install", target.ID, "--scope", entry.Scope, "-y"); err != nil {
					gone[id] = false
					if userScope {
						failures = append(failures, err)
					} else {
						keep(err)
					}
					continue
				}
			}
			if err := command(reason, dir, "uninstall", id, "--scope", entry.Scope, "--keep-data"); err != nil {
				gone[id] = false
				keep(err)
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
		if err := command("legacy marketplace of "+target.ID+" in "+knownPath+", nothing installed from it",
			"", "marketplace", "remove", marketplace); err != nil {
			keep(err)
		} else {
			delete(known, marketplace)
		}
	}
	for _, name := range append([]string{target.Name}, target.FormerNames()...) {
		owned, err := legacyDevDirOwned(home, name)
		if err != nil {
			keep(err)
		}
		devDir := legacyDevMarketplace(home, name)
		pointed := false
		for _, entry := range known {
			pointed = pointed ||
				entry.Source.Kind == claudeMarketplaceDirectory && filepath.Clean(entry.Source.Path) == devDir
		}
		if owned && !pointed && !wait(devDir) {
			if err := installer.change("remove "+devDir+": the legacy -dev marketplace of "+target.ID, func() error {
				return os.RemoveAll(devDir)
			}); err != nil {
				keep(err)
			}
		}
	}
	for _, former := range target.FormerNames() {
		if gone[former+"@"+pfmconfig.ProfessorMarketplace] {
			if err := installer.removeFormerProfessorCopy(home, former, target.ID, wait); err != nil {
				keep(err)
			}
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
		keep(err)
	} else if message != "" {
		if err := installer.change(message, edit); err != nil {
			keep(err)
		}
	}
	for _, err := range kept {
		message := fmt.Sprintf("claude plugin %s in %s: %v; left in place", target.ID, run.dir, err)
		installer.say("  warn    %s", message)
		installer.record("warn", message, err)
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
		for _, name := range append([]string{target.Name}, target.FormerNames()...) {
			owned, err := legacyDevDirOwned(home, name)
			if err != nil {
				failures = append(failures, fmt.Errorf("plugin %s: %w", target.ID, err))
			}
			if owned {
				left = append(left, legacyDevMarketplace(home, name))
			}
		}
		for _, former := range target.FormerNames() {
			copies, err := formerProfessorCopies(home, former)
			if err != nil {
				failures = append(failures, fmt.Errorf("plugin %s: %w", target.ID, err))
			}
			left = append(left, copies...)
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
