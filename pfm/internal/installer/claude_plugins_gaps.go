package installer

import (
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

	"github.com/rezzminator/professor/pfm/internal/claudeplugin"
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
// not enabled, every install-only plugin enabled, every local target whose
// copy is missing, not a plain directory or not the checkout's content, every
// other plugin enabled under two or more keys, and of retiredPlugins (the
// retired lists' plugin ids once ours, keyed by their successor's bare name)
// every enabled id, every listed bare name a successor retired, and every
// retired id's pluginConfigs entry left beside its successor's. A missing file
// returns ErrClaudeSettingsAbsent; a file that cannot be read or parsed
// returns its error, never an answer, because its real state is unknown.
func ClaudePluginGaps(
	path string,
	targets []ClaudePluginTarget,
	retiredPlugins map[string][]string,
) ([]ClaudePluginGap, error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w at %s", ErrClaudeSettingsAbsent, path)
	}
	document, err := claudeplugin.ReadSettings(path)
	if err != nil {
		return nil, err
	}
	var gaps []ClaudePluginGap
	for index := range targets {
		target := &targets[index]
		if successor := target.retiredSuccessor(retiredPlugins); successor != "" {
			// Its other gaps would send the reader to ensure the retired copy.
			gaps = append(gaps, retiredListedGap(target.Name, successor))
			continue
		}
		switch {
		case target.InstallOnly && pluginEnabled(document, target.ID):
			// Every -alpha install disables each plugin claude.plugins does not list.
			gaps = append(gaps, ClaudePluginGap{
				fmt.Sprintf(
					"plugin %s enabled (%s) in %s, but %s does not list it",
					target.Name, target.ID, path, pfmconfig.KeyClaudePlugins,
				),
				ClaudePluginRepair,
			})
		case !target.InstallOnly && !pluginEnabled(document, target.ID):
			gaps = append(gaps, ClaudePluginGap{"plugin " + target.ID + " not enabled in " + path, ClaudePluginRepair})
		}
		if target.Local {
			gap, err := claudePluginCopyGap(target)
			if err != nil {
				// One unreadable tree is that plugin's gap; the rest are still read.
				gap = target.Mirror() + " cannot be compared with " + target.Checkout + ": " + err.Error()
			}
			if gap != "" {
				gaps = append(gaps, ClaudePluginGap{"plugin " + target.ID + " copy " + gap, ClaudePluginRepair})
			}
		}
	}
	for _, successor := range slices.Sorted(maps.Keys(retiredPlugins)) {
		for _, id := range retiredPlugins[successor] {
			if pluginEnabled(document, id) {
				gaps = append(gaps, ClaudePluginGap{
					Problem: "plugin " + id + " retired (now " + successor + ") but still enabled in " + path,
					Fix: "claude plugin uninstall " + shellCommandLine(id) + " --scope user" +
						"; if claude.plugins still lists it, replace that entry with " + successor,
				})
			}
		}
	}
	for index := range targets {
		target := &targets[index]
		retired := retiredPlugins[target.Name]
		if !target.Own || len(retired) == 0 {
			continue
		}
		// Install carries the first entry to target.ID and drops them all
		// (retireConfigs); the plugin warns on each one it leaves.
		if ids := retiredConfigIDs(document, target, retired); len(ids) != 0 {
			shown := make([]string, len(ids))
			for position, id := range ids {
				shown[position] = EscapeControl(id)
			}
			gaps = append(gaps, ClaudePluginGap{
				Problem: "pluginConfigs of " + strings.Join(shown, " and ") + " still in " + path +
					": retired copies of " + target.ID,
				Fix: "pfm install copies the first to " + target.ID + " when it has none, then drops them",
			})
		}
	}
	return append(gaps, claudePluginDuplicateGaps(document, path, targets)...), nil
}

// retiredSuccessor is the plugin that replaced a listed bare name of ours, read
// from retiredPlugins; "" for a full id, an install-only plugin or a name no
// successor retired.
func (target ClaudePluginTarget) retiredSuccessor(retiredPlugins map[string][]string) string {
	if !target.Own || target.InstallOnly {
		return ""
	}
	for _, successor := range slices.Sorted(maps.Keys(retiredPlugins)) {
		for _, id := range retiredPlugins[successor] {
			// A successor's own name under a retired marketplace ({successor}@{old}) retires that id alone.
			if name, _, _ := strings.Cut(id, "@"); name == target.Name && successor != target.Name {
				return successor
			}
		}
	}
	return ""
}

// retiredListedGap is the gap install fails and doctor reports for a
// claude.plugins bare name its successor retired: ensuring it would enable the
// copy the successor's install disables.
func retiredListedGap(name, successor string) ClaudePluginGap {
	return ClaudePluginGap{
		Problem: "plugin " + name + " is retired (now " + successor + ") but " + pfmconfig.KeyClaudePlugins + " lists it",
		Fix:     "replace " + name + " with " + successor + " in " + pfmconfig.KeyClaudePlugins + ", then " + ClaudePluginRepair,
	}
}

// ClaudePluginsNotInstalled reads one account's plugins/installed_plugins.json
// and names every target id with no install record whose installPath
// exists. Accounts may share one settings.json, so this record — one per
// config dir — is what says a plugin is installed in THIS account. A missing
// file means none is installed; a file that cannot be read or parsed returns
// its error, never an answer.
func ClaudePluginsNotInstalled(configDir string, targets []ClaudePluginTarget) ([]string, error) {
	record, err := claudeplugin.ReadInstalls(configDir)
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

// ClaudePluginRecordedVersions is every version configDir's install record
// holds for id, one per entry naming one, control characters escaped; none
// when id has no record. A record that cannot be read returns its error, never
// an answer.
func ClaudePluginRecordedVersions(configDir, id string) ([]string, error) {
	record, err := claudeplugin.ReadInstalls(configDir)
	if err != nil {
		return nil, err
	}
	versions := make([]string, 0, len(record[id]))
	for _, entry := range record[id] {
		if entry.Version != "" {
			versions = append(versions, EscapeControl(entry.Version))
		}
	}
	return versions, nil
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
	document, err := claudeplugin.ReadSettings(filepath.Join(configDir, "settings.json"))
	if err != nil {
		return nil, err
	}
	missing, err := ClaudePluginsNotInstalled(configDir, candidates)
	if err != nil {
		return nil, err
	}
	known, err := claudeplugin.KnownMarketplaceNames(configDir)
	if err != nil {
		return nil, err
	}
	unresolved := map[string]ClaudePluginGap{}
	for index := range candidates {
		target := &candidates[index]
		if pluginEnabled(document, target.ID) && !slices.Contains(missing, target.ID) {
			continue
		}
		if known[target.MarketplaceName] {
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
// install enables each listed key again. A key from the file prints with its
// control characters escaped.
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
	for index := range targets {
		listed[targets[index].ID] = true
	}
	var gaps []ClaudePluginGap
	for _, name := range slices.Sorted(maps.Keys(byName)) {
		ids := byName[name]
		if len(ids) < 2 {
			continue
		}
		slices.Sort(ids)
		if !slices.ContainsFunc(ids, func(id string) bool { return !listed[id] }) {
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
