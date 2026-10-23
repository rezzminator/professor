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
	if proc.engine == engineCodex && args[0] == "plugin" {
		return answerCodexPluginCLI(proc), true
	}
	if proc.engine != engineClaude || args[0] != "plugin" {
		return 0, false
	}
	switch {
	case len(args) == 5 && args[1] == "uninstall" && args[3] == claudeScopeFlag && args[4] == claudeUserScope:
		if err := removeClaudePlugin(claudeConfigDir(proc), args[2]); err != nil {
			fmt.Fprintf(proc.stderr, "mock-engine: %v\n", err)
			return ExitUsage, true
		}
		return 0, true
	case len(args) == 4 && args[1] == "marketplace" && args[2] == "add":
		configDir := claudeConfigDir(proc)
		name, err := recordClaudeMarketplace(configDir, args[3])
		if err != nil {
			fmt.Fprintf(proc.stderr, "mock-engine: %v\n", err)
			return ExitUsage, true
		}
		if name == "" {
			fmt.Fprintf(
				proc.stdout,
				"mock-engine: claude plugin marketplace add %s — fixture, nothing fetched\n",
				args[3],
			)
			return 0, true
		}
		fmt.Fprintf(
			proc.stdout,
			"mock-engine: claude plugin marketplace add %s — fixture, recorded directory marketplace %s in %s\n",
			args[3],
			name,
			configDir,
		)
		return 0, true
	case (len(args) == 3 || len(args) == 4 && args[3] == "-y" || len(args) == 6 && args[3] == "-y" && args[4] == claudeScopeFlag && args[5] == claudeUserScope) && args[1] == "install":
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
	case len(args) == 6 && args[1] == "update" && args[3] == claudeScopeFlag && args[4] == claudeUserScope &&
		args[5] == "-y":
		configDir := claudeConfigDir(proc)
		if err := updateClaudePlugin(configDir, args[2]); err != nil {
			fmt.Fprintf(proc.stderr, "mock-engine: %v\n", err)
			return ExitUsage, true
		}
		fmt.Fprintf(
			proc.stdout,
			"mock-engine: claude plugin update %s — fixture, recorded its checkout version in %s\n",
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

// The fixture plays Claude Code's plugin record: `plugin marketplace add` of
// an absolute directory holding .claude-plugin/marketplace.json records it in
// plugins/known_marketplaces.json; `plugin install` (user scope) of a plugin
// from such a marketplace snapshots its directory into
// plugins/cache/{marketplace}/{name}/{version} and records scope, installPath
// and version in plugins/installed_plugins.json; `plugin update --scope user`
// moves that entry to the directory's current version. A marketplace the
// fixture cannot read (a GitHub source) fetches nothing: its install records an
// empty snapshot at plugins/cache/{id} with no version.

const (
	claudeScopeFlag        = "--scope"
	claudeUserScope        = "user"
	claudeSourceKey        = "source"
	claudeDirectorySource  = "directory"
	claudeUnknownVersion   = "unknown"
	claudeMarketplaceFile  = "marketplace.json"
	claudePluginManifest   = "plugin.json"
	claudeManifestDir      = ".claude-plugin"
	claudeKnownMarketplace = "known_marketplaces.json"
)

// writePluginDocument writes document as indented JSON, mode 0600.
func writePluginDocument(path string, document map[string]any) error {
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// recordClaudeMarketplace records source as a directory marketplace under its
// manifest's name and returns that name; "" when source is not an absolute
// directory holding a marketplace manifest, which records nothing.
func recordClaudeMarketplace(configDir, source string) (string, error) {
	if !filepath.IsAbs(source) {
		return "", nil
	}
	manifest, err := readPluginDocument(filepath.Join(source, claudeManifestDir, claudeMarketplaceFile))
	if err != nil {
		return "", err
	}
	name, _ := manifest["name"].(string)
	if name == "" {
		return "", nil
	}
	path := filepath.Join(configDir, "plugins", claudeKnownMarketplace)
	known, err := readPluginDocument(path)
	if err != nil {
		return "", err
	}
	known[name] = map[string]any{
		claudeSourceKey:   map[string]any{claudeSourceKey: claudeDirectorySource, "path": source},
		"installLocation": source,
	}
	return name, writePluginDocument(path, known)
}

// claudePluginSource is the directory a recorded directory marketplace serves
// plugin name from and that plugin's version; ok false when the marketplace is
// not a recorded directory.
func claudePluginSource(configDir, marketplace, name string) (dir, version string, ok bool, err error) {
	known, err := readPluginDocument(filepath.Join(configDir, "plugins", claudeKnownMarketplace))
	if err != nil {
		return "", "", false, err
	}
	entry, _ := known[marketplace].(map[string]any)
	source, _ := entry[claudeSourceKey].(map[string]any)
	root, _ := source["path"].(string)
	if source[claudeSourceKey] != claudeDirectorySource || root == "" {
		return "", "", false, nil
	}
	manifestPath := filepath.Join(root, claudeManifestDir, claudeMarketplaceFile)
	manifest, err := readPluginDocument(manifestPath)
	if err != nil {
		return "", "", false, err
	}
	plugins, _ := manifest["plugins"].([]any)
	for _, raw := range plugins {
		plugin, _ := raw.(map[string]any)
		if plugin["name"] != name {
			continue
		}
		relative, isString := plugin[claudeSourceKey].(string)
		if !isString || filepath.IsAbs(relative) {
			return "", "", false, fmt.Errorf("%s: plugin %s source is not a relative path", manifestPath, name)
		}
		dir = filepath.Join(root, relative)
		pluginManifest, err := readPluginDocument(filepath.Join(dir, claudeManifestDir, claudePluginManifest))
		if err != nil {
			return "", "", false, err
		}
		for _, candidate := range []any{pluginManifest["version"], plugin["version"], claudeUnknownVersion} {
			if text, _ := candidate.(string); text != "" {
				return dir, text, true, nil
			}
		}
	}
	return "", "", false, fmt.Errorf("%s names no plugin %s", manifestPath, name)
}

// claudePluginSnapshot is the user-scope install entry for id: a copy of a
// directory marketplace's plugin at its version, made once per version, or an
// empty snapshot with no version for a marketplace the fixture cannot read.
func claudePluginSnapshot(configDir, id string) (map[string]any, error) {
	name, marketplace, _ := strings.Cut(id, "@")
	dir, version, ok, err := claudePluginSource(configDir, marketplace, name)
	if err != nil {
		return nil, err
	}
	if !ok {
		cachePath := filepath.Join(configDir, "plugins", "cache", id)
		if err := os.MkdirAll(cachePath, 0o700); err != nil {
			return nil, fmt.Errorf("create %s: %w", cachePath, err)
		}
		return map[string]any{"scope": claudeUserScope, "installPath": cachePath}, nil
	}
	snapshot := filepath.Join(configDir, "plugins", "cache", marketplace, name, version)
	if _, err := os.Stat(snapshot); os.IsNotExist(err) {
		staging := snapshot + ".partial"
		if err := os.RemoveAll(staging); err != nil {
			return nil, fmt.Errorf("clear %s: %w", staging, err)
		}
		if err := os.CopyFS(staging, os.DirFS(dir)); err != nil {
			return nil, fmt.Errorf("copy %s to %s: %w", dir, staging, err)
		}
		if err := os.Rename(staging, snapshot); err != nil {
			return nil, fmt.Errorf("rename %s: %w", staging, err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", snapshot, err)
	}
	return map[string]any{"scope": claudeUserScope, "installPath": snapshot, "version": version}, nil
}

// setClaudePluginEntry replaces id's user-scope entry in the install record
// with entry, keeping every other scope's; with mustExist, an id without a
// user-scope entry is an error.
func setClaudePluginEntry(installed map[string]any, id string, entry map[string]any, mustExist bool) error {
	plugins, _ := installed["plugins"].(map[string]any)
	if plugins == nil {
		plugins = map[string]any{}
	}
	entries, _ := plugins[id].([]any)
	kept := []any{}
	found := false
	for _, raw := range entries {
		if existing, _ := raw.(map[string]any); existing["scope"] == claudeUserScope {
			found = true
			continue
		}
		kept = append(kept, raw)
	}
	if mustExist && !found {
		return fmt.Errorf("plugin %s is not installed in scope %s", id, claudeUserScope)
	}
	plugins[id] = append(kept, entry)
	installed["plugins"] = plugins
	return nil
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
	entry, err := claudePluginSnapshot(configDir, id)
	if err != nil {
		return err
	}
	if err := setClaudePluginEntry(installed, id, entry, false); err != nil {
		return err
	}
	if err := writePluginDocument(settingsPath, settings); err != nil {
		return err
	}
	return writePluginDocument(installedPath, installed)
}

// updateClaudePlugin moves id's user-scope entry to its marketplace's current
// version.
func updateClaudePlugin(configDir, id string) error {
	installedPath := filepath.Join(configDir, "plugins", "installed_plugins.json")
	installed, err := readPluginDocument(installedPath)
	if err != nil {
		return err
	}
	entry, err := claudePluginSnapshot(configDir, id)
	if err != nil {
		return err
	}
	if err := setClaudePluginEntry(installed, id, entry, true); err != nil {
		return err
	}
	return writePluginDocument(installedPath, installed)
}

func removeClaudePlugin(configDir, id string) error {
	path := filepath.Join(configDir, "plugins", "installed_plugins.json")
	installed, err := readPluginDocument(path)
	if err != nil {
		return err
	}
	plugins, _ := installed["plugins"].(map[string]any)
	entries, _ := plugins[id].([]any)
	kept := []any{}
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		if entry["scope"] != claudeUserScope {
			kept = append(kept, raw)
			continue
		}
		cache, _ := entry["installPath"].(string)
		if cache != "" {
			if err := os.RemoveAll(cache); err != nil {
				return fmt.Errorf("remove fixture plugin cache %s: %w", cache, err)
			}
		}
	}
	if len(kept) > 0 {
		plugins[id] = kept
	} else {
		delete(plugins, id)
	}
	if err := writePluginDocument(path, installed); err != nil {
		return err
	}
	settingsPath := filepath.Join(configDir, "settings.json")
	settings, err := readPluginDocument(settingsPath)
	if err != nil {
		return err
	}
	enabled, _ := settings["enabledPlugins"].(map[string]any)
	delete(enabled, id)
	return writePluginDocument(settingsPath, settings)
}
