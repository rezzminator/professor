package hostcheck

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
)

const (
	checkFunctionHookModules = "function-hook-modules"
	hookModulesFlagKey       = "tengu_plugin_hooks_modules"
	growthBookCacheKey       = "cachedGrowthBookFeatures"
)

// functionHookModules reads, per account, the GrowthBook rollout flag Claude
// Code caches in {configDir}/.claude.json and gates every plugin hook module
// on. A cached false, or an unset flag while a function-hook plugin is
// installed, means those plugins list as enabled and never run: a gateway
// host never refreshes the cache, and the launch's function-hooks variable
// does not override it. A read that fails is a row, never a clean answer.
func functionHookModules(env Env) ([]Row, error) {
	var rows []Row
	for _, dir := range accountConfigDirs(env) {
		accountHookModules(&rows, dir)
	}
	return rows, nil
}

// accountConfigDirs is every account's config dir, or the first account's
// default dir on a host with no roster.
func accountConfigDirs(env Env) []string {
	var dirs []string
	for _, account := range sortedAccounts(env) {
		dirs = append(dirs, account.ConfigDir)
	}
	if len(dirs) == 0 {
		dirs = []string{firstAccountDir(env)}
	}
	return uniquePaths(dirs)
}

func accountHookModules(rows *[]Row, dir string) {
	path := filepath.Join(dir, ".claude.json")
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		*rows = append(*rows, unreadable(checkFunctionHookModules, path, err))
		return
	}
	stateExists := err == nil
	var flag json.RawMessage
	if stateExists {
		var state struct {
			Features map[string]json.RawMessage `json:"cachedGrowthBookFeatures"`
		}
		if err := json.Unmarshal(raw, &state); err != nil {
			*rows = append(*rows, unparsable(checkFunctionHookModules, path, err,
				"whether Claude Code loads function-hook plugin modules in this account is unknown"))
			return
		}
		flag = state.Features[hookModulesFlagKey]
	}
	if string(flag) == "true" {
		return
	}
	plugins, ok := modulePlugins(rows, dir)
	if !ok {
		return
	}
	fix := fmt.Sprintf(
		"with no Claude chat running in this account (pfm chat ls), set %s.%s to true in %s, then start a new chat; "+
			"a later GrowthBook fetch that serves false undoes it",
		growthBookCacheKey, hookModulesFlagKey, path,
	)
	override := claudelaunch.FunctionHooksEnv + "=1 does not override"
	switch {
	case len(flag) != 0:
		installed := ""
		if len(plugins) != 0 {
			installed = " (installed: " + strings.Join(plugins, ", ") + ")"
		}
		*rows = append(*rows, Row{Warn, checkFunctionHookModules, path, fmt.Sprintf(
			"%s.%s is %s: Claude Code loads no function-hook plugin module in this account%s; "+
				"a gateway host never refreshes this GrowthBook cache, and %s a cached false",
			growthBookCacheKey, hookModulesFlagKey, flag, installed, override,
		), fix})
	case len(plugins) == 0:
	case !stateExists:
		*rows = append(*rows, Row{Warn, checkFunctionHookModules, path, fmt.Sprintf(
			"%s does not exist, so %s.%s is unset while function-hook plugins %s are installed: "+
				"Claude Code loads none of their modules until a GrowthBook fetch sets it, "+
				"which never succeeds behind a gateway, and %s an unset flag",
			path, growthBookCacheKey, hookModulesFlagKey, strings.Join(plugins, ", "), override,
		), "start one chat in this account so Claude Code writes " + path + ", then " + fix})
	default:
		*rows = append(*rows, Row{Warn, checkFunctionHookModules, path, fmt.Sprintf(
			"%s.%s is unset while function-hook plugins %s are installed: "+
				"Claude Code loads none of their modules until a GrowthBook fetch sets it, "+
				"which never succeeds behind a gateway, and %s an unset flag",
			growthBookCacheKey, hookModulesFlagKey, strings.Join(plugins, ", "), override,
		), fix})
	}
}

// modulePlugins names, sorted, every plugin dir's installed_plugins.json
// records whose hooks/hooks.json declares hook modules. ok is false when a
// read failed; its row is already appended.
func modulePlugins(rows *[]Row, dir string) ([]string, bool) {
	record := filepath.Join(dir, "plugins", "installed_plugins.json")
	before := len(*rows)
	raw, found := readFile(rows, checkFunctionHookModules, record)
	if !found {
		return nil, len(*rows) == before
	}
	var installed struct {
		Plugins map[string][]struct {
			InstallPath string `json:"installPath"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(raw, &installed); err != nil {
		*rows = append(*rows, unparsable(checkFunctionHookModules, record, err,
			"which function-hook plugins are installed is unknown"))
		return nil, false
	}
	markets, ok := directoryMarketplaces(rows, dir)
	if !ok {
		return nil, false
	}
	var names []string
	for id, entries := range installed.Plugins {
		var folders []string
		name, market, _ := strings.Cut(id, "@")
		if root, directory := markets[market]; directory {
			folder, ok := directoryPluginFolder(rows, root, name, id)
			if !ok {
				continue
			}
			folders = []string{folder}
		} else {
			for _, entry := range entries {
				if entry.InstallPath != "" {
					folders = append(folders, entry.InstallPath)
				}
			}
		}
		for _, folder := range folders {
			hooksPath := filepath.Join(folder, "hooks", "hooks.json")
			hooks, found := readFile(rows, checkFunctionHookModules, hooksPath)
			if !found {
				continue
			}
			var declared struct {
				Modules []string `json:"modules"`
			}
			if err := json.Unmarshal(hooks, &declared); err != nil {
				*rows = append(*rows, unparsable(checkFunctionHookModules, hooksPath, err,
					"whether plugin "+id+" carries hook modules is unknown"))
				continue
			}
			if len(declared.Modules) != 0 {
				names = append(names, id)
				break
			}
		}
	}
	sort.Strings(names)
	return names, len(*rows) == before
}

// directoryMarketplaces maps each directory-source marketplace in dir's
// plugins/known_marketplaces.json to its path: Claude Code runs such a
// marketplace's plugins from that folder, not from their recorded installPath.
// ok is false when the read failed; its row is already appended.
func directoryMarketplaces(rows *[]Row, dir string) (map[string]string, bool) {
	record := filepath.Join(dir, "plugins", "known_marketplaces.json")
	before := len(*rows)
	raw, found := readFile(rows, checkFunctionHookModules, record)
	if !found {
		return nil, len(*rows) == before
	}
	var known map[string]struct {
		Source struct {
			Kind string `json:"source"`
			Path string `json:"path"`
		} `json:"source"`
	}
	if err := json.Unmarshal(raw, &known); err != nil {
		*rows = append(*rows, unparsable(checkFunctionHookModules, record, err,
			"which marketplaces run their plugins from a folder is unknown"))
		return nil, false
	}
	markets := map[string]string{}
	for name, market := range known {
		if market.Source.Kind == "directory" {
			markets[name] = market.Source.Path
		}
	}
	return markets, true
}

// directoryPluginFolder is the folder inside root that plugin id's entry in
// root/.claude-plugin/marketplace.json names. ok is false when it cannot be
// resolved; its row is already appended.
func directoryPluginFolder(rows *[]Row, root, name, id string) (string, bool) {
	manifest := filepath.Join(root, ".claude-plugin", "marketplace.json")
	unknown := "which folder Claude Code loads plugin " + id + " from, and so its hook modules, is unknown"
	before := len(*rows)
	raw, found := readFile(rows, checkFunctionHookModules, manifest)
	if !found {
		if len(*rows) == before {
			fix := "rerun pfm install so the marketplace folder " + root + " is rebuilt"
			problem := manifest + " does not exist — " + unknown
			*rows = append(*rows, Row{Warn, checkFunctionHookModules, manifest, problem, fix})
		}
		return "", false
	}
	var listed struct {
		Plugins []struct {
			Name   string `json:"name"`
			Source any    `json:"source"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		*rows = append(*rows, unparsable(checkFunctionHookModules, manifest, err, unknown))
		return "", false
	}
	for _, entry := range listed.Plugins {
		if source, isPath := entry.Source.(string); entry.Name == name && isPath {
			return filepath.Join(root, source), true
		}
	}
	problem := fmt.Sprintf("%s lists no folder source for plugin %s — %s", manifest, name, unknown)
	fix := "rerun pfm install so the marketplace lists " + name
	*rows = append(*rows, Row{Warn, checkFunctionHookModules, manifest, problem, fix})
	return "", false
}

// unparsable is the row for a file that was read but could not be parsed:
// the check failed to look, which is never a clean answer.
func unparsable(check, path string, err error, unknown string) Row {
	return Row{
		Warn,
		check,
		path,
		fmt.Sprintf("cannot parse %s: %v — %s", path, err, unknown),
		"repair the JSON in " + path + ", then rerun pfm doctor",
	}
}
