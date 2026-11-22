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
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/claudeplugin"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// unwireClaudeSettings is uninstall's take-back of what install wrote into the
// Claude settings and plugin records: the professor plugins (unwireClaudePlugins)
// and pfm's wrapping of Herdr's Claude hook (wireHerdrClaudeIntegration).
func (installer *engine) unwireClaudeSettings(ctx context.Context) error {
	return errors.Join(installer.unwireClaudePlugins(), installer.wireHerdrClaudeIntegration(ctx))
}

// unwireClaudePlugins takes back what ensureClaudePlugins wrote, in every Claude
// config dir install covers, each physical file once: each {plugin}@professor key
// of settings.json's enabledPlugins (an emptied member goes too) and of
// plugins/installed_plugins.json, the professor record of known_marketplaces.json,
// the copies Claude made for them, and last the pfm-owned directory marketplace.
// Another marketplace's plugin is the owner's and stays. A dir that fails keeps its
// copies and the directory marketplace, and costs no other dir.
func (installer *engine) unwireClaudePlugins() error {
	seen := map[string]bool{}
	var failed error
	for _, dir := range installer.claudePluginDirs() {
		if err := installer.takeClaudePluginDir(dir, seen, professorTake); err != nil {
			failed = errors.Join(failed, installer.pluginStepFailure(dir, err))
		}
	}
	if failed != nil {
		return failed
	}
	return installer.removeDevMarketplace()
}

// claudePluginDirs are the primary account's dir, where the plugin step runs,
// then every roster dir; ConfigDir on a host with neither.
func (installer *engine) claudePluginDirs() []string {
	dirs := pfmconfig.Config{Accounts: installer.options.ClaudeAccounts}.ClaudeConfigDirs()
	if primary := installer.options.PrimaryConfigDir; primary != "" && !slices.Contains(dirs, primary) {
		dirs = append([]string{primary}, dirs...)
	}
	if len(dirs) == 0 {
		dirs = []string{installer.options.ConfigDir}
	}
	return dirs
}

func professorPluginID(key string) bool {
	return strings.HasSuffix(key, "@"+pfmconfig.ProfessorMarketplace)
}

// professorTake is uninstall's take-back of every professor plugin.
var professorTake = claudePluginTake{
	edits: claudeRecordEdits, copies: professorCopies, within: []string{claudePluginCopies, "marketplaces"},
}

var claudeRecordEdits = []claudeRecordEdit{
	{file: claudeSettingsName, member: "enabledPlugins", ours: professorPluginID, dropEmpty: true},
	{file: filepath.Join(claudePlugins, "installed_plugins.json"), member: claudePlugins, ours: professorPluginID},
	{file: filepath.Join(claudePlugins, "known_marketplaces.json"), ours: func(key string) bool {
		return key == pfmconfig.ProfessorMarketplace
	}},
}

// professorCopies lists each installPath of dir's {plugin}@professor records, the
// installLocation of a professor marketplace that is no directory source (a
// directory is removeDevMarketplace's), then plugins/cache/professor.
func professorCopies(dir string) ([]string, error) {
	installs, err := claudeplugin.ReadInstalls(dir)
	if err != nil {
		return nil, err
	}
	_, known, err := claudeplugin.ReadKnownMarketplaces(dir)
	if err != nil {
		return nil, err
	}
	var copies []string
	for id, records := range installs {
		if !professorPluginID(id) {
			continue
		}
		for _, record := range records {
			copies = append(copies, record.InstallPath)
		}
	}
	sort.Strings(copies)
	market, ok := known[pfmconfig.ProfessorMarketplace]
	if ok && market.Source.Kind != claudeplugin.MarketplaceDirectory {
		copies = append(copies, market.InstallLocation)
	}
	return append(copies, filepath.Join(dir, claudePlugins, claudePluginCopies, pfmconfig.ProfessorMarketplace)), nil
}

// removeDevMarketplace removes the pfm-owned directory marketplace once its manifest
// names it professor; a directory there holding anything else stays, named.
func (installer *engine) removeDevMarketplace() error {
	dir := professorDevMarketplace(installer.options.Home)
	if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return installer.fail(fmt.Errorf("claude plugins: inspect %s: %w", dir, err))
	}
	manifest := filepath.Join(dir, ".claude-plugin", "marketplace.json")
	raw, err := os.ReadFile(manifest)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return installer.fail(fmt.Errorf("claude plugins: read %s: %w", manifest, err))
	}
	var market claudeplugin.Marketplace
	if err == nil {
		err = json.Unmarshal(raw, &market)
	}
	if err != nil || market.Name != pfmconfig.ProfessorMarketplace {
		installer.skip(fmt.Sprintf(
			"claude plugins: %s is not the %s marketplace pfm writes (%s: name %q, %v), kept",
			dir, pfmconfig.ProfessorMarketplace, manifest, market.Name, err,
		))
		return nil
	}
	installer.markRemoved(dir)
	return installer.change("remove "+dir+" (pfm's directory marketplace)", func() error { return os.RemoveAll(dir) })
}
