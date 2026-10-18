package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// ClaudePluginGap is one plugin problem pfm doctor reports and its repair.
type ClaudePluginGap struct {
	Problem string
	Fix     string
}

// ErrClaudeSettingsAbsent is ClaudePluginGaps' answer for a store with no
// settings.json: a store never set up, not one missing its plugins.
var ErrClaudeSettingsAbsent = errors.New("no settings.json")

// ClaudePluginGaps reads the store's settings.json and names every target id
// not enabled, every plugin of ours enabled under two or more of its copies'
// ids (GitHub, professor, -dev), every install-only plugin enabled under any,
// every local target whose copy is missing, not a plain directory or not the
// checkout's content, every other plugin enabled under two or more keys, and
// every enabled retired default plugin copy. A
// missing file returns ErrClaudeSettingsAbsent; a file that cannot be read or
// parsed returns its error, never an answer, because its real state is unknown.
func ClaudePluginGaps(path string, targets []ClaudePluginTarget) ([]ClaudePluginGap, error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w at %s", ErrClaudeSettingsAbsent, path)
	}
	document, err := readClaudeSettingsDocument(path)
	if err != nil {
		return nil, err
	}
	var gaps []ClaudePluginGap
	for index := range targets {
		target := &targets[index]
		var enabledCopies []string
		for _, id := range target.copyIDs() {
			if pluginEnabled(document, id) {
				enabledCopies = append(enabledCopies, id)
			}
		}
		if target.InstallOnly {
			if len(enabledCopies) != 0 {
				// Every -alpha install disables each copy claude.plugins does not list.
				gaps = append(gaps, ClaudePluginGap{
					fmt.Sprintf(
						"plugin %s enabled (%s) in %s, but %s does not list it",
						target.Name, strings.Join(enabledCopies, " and "), path, pfmconfig.KeyClaudePlugins,
					),
					ClaudePluginRepair,
				})
			}
		} else {
			if !pluginEnabled(document, target.ID) {
				gaps = append(
					gaps,
					ClaudePluginGap{"plugin " + target.ID + " not enabled in " + path, ClaudePluginRepair},
				)
			}
			if len(enabledCopies) > 1 {
				// Every build's install disables the copies it does not ensure.
				gaps = append(gaps, ClaudePluginGap{
					fmt.Sprintf(
						"plugin %s enabled %s (%s) in %s",
						target.Name, timesWord(len(enabledCopies)), strings.Join(enabledCopies, " and "), path,
					),
					ClaudePluginRepair,
				})
			}
		}
		if target.Local {
			gap, err := claudePluginCopyGap(target)
			if err != nil {
				// One unreadable tree is that plugin's gap; the rest are still read.
				gap = target.Mirror() + " cannot be compared with " + target.Checkout + ": " + err.Error()
			}
			if gap != "" {
				gaps = append(gaps, ClaudePluginGap{"plugin " + target.DevID + " copy " + gap, ClaudePluginRepair})
			}
		}
	}
	for _, successor := range slices.Sorted(maps.Keys(retiredClaudePlugins)) {
		for _, id := range retiredClaudePlugins[successor] {
			if pluginEnabled(document, id) {
				gaps = append(gaps, ClaudePluginGap{
					Problem: "plugin " + id + " retired (now " + successor + ") but still enabled in " + path,
					Fix: "claude plugin uninstall " + shellCommandLine(id) + " --scope user" +
						"; if claude.plugins still lists it, replace that entry with " + successor,
				})
			}
		}
	}
	return append(gaps, claudePluginDuplicateGaps(document, path, targets)...), nil
}

// ClaudePluginsNotInstalled reads one account's plugins/installed_plugins.json
// and names every target id with no install record whose installPath
// exists. Accounts may share one settings.json, so this record — one per
// config dir — is what says a plugin is installed in THIS account. A missing
// file means none is installed; a file that cannot be read or parsed returns
// its error, never an answer.
func ClaudePluginsNotInstalled(configDir string, targets []ClaudePluginTarget) ([]string, error) {
	record, err := readClaudePluginInstalls(configDir)
	if err != nil {
		return nil, err
	}
	var missing []string
	for index := range targets {
		target := &targets[index]
		installed := false
		for _, entry := range record[target.ID] {
			if info, statErr := os.Stat(entry.InstallPath); entry.InstallPath != "" && statErr == nil && info.IsDir() {
				installed = true
				break
			}
		}
		if !installed {
			missing = append(missing, target.ID)
		}
	}
	return missing, nil
}

// claudePluginInstall is one install record Claude Code keeps for a plugin id
// in plugins/installed_plugins.json: one per scope it is installed in, a
// project or local one naming its project. Claude Code owns the shape: a
// scope, projectPath or version that is not a string reads as empty rather
// than failing the record every other reader needs; installPath stays strict.
type claudePluginInstall struct {
	Scope       string
	ProjectPath string
	InstallPath string
	Version     string
}

// UnmarshalJSON reads one entry, its loose fields as recordString does.
func (install *claudePluginInstall) UnmarshalJSON(raw []byte) error {
	var entry struct {
		Scope       json.RawMessage `json:"scope"`
		ProjectPath json.RawMessage `json:"projectPath"`
		InstallPath string          `json:"installPath"`
		Version     json.RawMessage `json:"version"`
	}
	if err := json.Unmarshal(raw, &entry); err != nil {
		return err
	}
	*install = claudePluginInstall{
		Scope:       recordString(entry.Scope),
		ProjectPath: recordString(entry.ProjectPath),
		InstallPath: entry.InstallPath,
		Version:     recordString(entry.Version),
	}
	return nil
}

// recordString is a raw record value as a string; empty when it is absent or
// not a JSON string.
func recordString(raw json.RawMessage) string {
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return ""
	}
	return text
}

// version is the record's version string; empty when it names none or names
// one that is not a JSON string.
func (install claudePluginInstall) version() string { return install.Version }

// readClaudePluginInstalls reads configDir's plugins/installed_plugins.json,
// keyed by plugin id. A missing file means none is installed; a file that
// cannot be read or parsed returns its error, never an answer.
func readClaudePluginInstalls(configDir string) (map[string][]claudePluginInstall, error) {
	path := filepath.Join(configDir, "plugins", "installed_plugins.json")
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var record struct {
		Plugins map[string][]claudePluginInstall `json:"plugins"`
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return record.Plugins, nil
}

// ClaudePluginsUnresolved names every listed full id pfm cannot install: still
// to be installed or enabled in configDir, its entry without a source, and its
// marketplace not among configDir's plugins/known_marketplaces.json. Claude
// Code resolves such an id only against a marketplace it already knows, so
// install and doctor report it by name with the reason and the repair, keyed by
// id. A missing known_marketplaces.json means none is known; a file that cannot
// be read or parsed returns its error, never an answer. Nothing is read when no
// target is a candidate.
func ClaudePluginsUnresolved(configDir string, targets []ClaudePluginTarget) (map[string]ClaudePluginGap, error) {
	var candidates []ClaudePluginTarget
	for index := range targets {
		if target := targets[index]; target.NeedsKnownMarketplace() {
			candidates = append(candidates, target)
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	document, err := readClaudeSettingsDocument(filepath.Join(configDir, "settings.json"))
	if err != nil {
		return nil, err
	}
	missing, err := ClaudePluginsNotInstalled(configDir, candidates)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(configDir, "plugins", "known_marketplaces.json")
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	known := map[string]json.RawMessage{}
	if err == nil {
		if err := json.Unmarshal(raw, &known); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	}
	unresolved := map[string]ClaudePluginGap{}
	for index := range candidates {
		target := &candidates[index]
		if pluginEnabled(document, target.ID) && !slices.Contains(missing, target.ID) {
			continue
		}
		if _, ok := known[target.MarketplaceName]; ok {
			continue
		}
		unresolved[target.ID] = ClaudePluginGap{
			Problem: "marketplace " + target.MarketplaceName +
				" is not known to Claude Code and its " + pfmconfig.KeyClaudePlugins + " entry has no source",
			Fix: ClaudePluginUnresolvedFix,
		}
	}
	return unresolved, nil
}

// claudePluginDuplicateGaps names every plugin enabled under two or more
// name@marketplace keys in document, managed by pfm or not, sorted by name. A
// plugin whose every enabled key claude.plugins lists is left alone: pfm
// install enables each listed key again. A plugin of ours enabled only under
// its own copies' ids (GitHub, professor, -dev) is left to ClaudePluginGaps'
// own gaps, whose repair pfm install owns. A key from the file prints with its control
// characters escaped.
func claudePluginDuplicateGaps(document map[string]any, path string, targets []ClaudePluginTarget) []ClaudePluginGap {
	enabled, _ := document["enabledPlugins"].(map[string]any)
	byName := map[string][]string{}
	for id, value := range enabled {
		if value != true {
			continue
		}
		name, _, _ := strings.Cut(id, "@")
		byName[name] = append(byName[name], id)
	}
	listed := map[string]bool{}
	managed := map[string][]string{}
	for index := range targets {
		listed[targets[index].ID] = true
		if targets[index].Own {
			managed[targets[index].Name] = targets[index].copyIDs()
		}
	}
	var gaps []ClaudePluginGap
	for _, name := range slices.Sorted(maps.Keys(byName)) {
		ids := byName[name]
		if len(ids) < 2 {
			continue
		}
		slices.Sort(ids)
		ours := !slices.ContainsFunc(ids, func(id string) bool { return !slices.Contains(managed[name], id) })
		if ours || !slices.ContainsFunc(ids, func(id string) bool { return !listed[id] }) {
			continue
		}
		shown := make([]string, len(ids))
		for index, id := range ids {
			shown[index] = EscapeControl(id)
		}
		gaps = append(gaps, ClaudePluginGap{
			fmt.Sprintf(
				"plugin %s enabled %s (%s) in %s",
				EscapeControl(name), timesWord(len(ids)), strings.Join(shown, " and "), path,
			),
			"keep one copy: claude plugin disable each other key",
		})
	}
	return gaps
}

// EscapeControl is text read from a file as one printed line: each control
// character escaped as Go spells it, so a newline in a key or a url cannot
// forge a line of its own.
func EscapeControl(text string) string {
	if !strings.ContainsFunc(text, unicode.IsControl) {
		return text
	}
	quoted := strconv.Quote(text)
	return quoted[1 : len(quoted)-1]
}

// timesWord spells how many keys enable one plugin.
func timesWord(count int) string {
	if count == 2 {
		return "twice"
	}
	return strconv.Itoa(count) + " times"
}
