package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/claudeplugin"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// extraKnownMarketplacesKey is the settings key Claude Code records each added
// marketplace under: `plugin marketplace add` writes
// extraKnownMarketplaces.{name} = {"source": {its source record}} into the
// user settings of the config dir it runs in, and refuses an add whose source
// kind differs from that entry in user or managed settings ("its source
// doesn't match its extraKnownMarketplaces entry").
const extraKnownMarketplacesKey = "extraKnownMarketplaces"

// ensureProfessorMarketplaceEntry points the professor marketplace's settings
// entry at this pass's source before any add of it runs, so a switch of
// claude.pluginSource between the checkout copies' directory and GitHub
// re-points the marketplace in place, its plugins and their saved options
// kept: `plugin marketplace remove`, the only other way past the refusal,
// uninstalls every plugin of the marketplace and deletes their options,
// secrets and data, and pfm never runs it. Every plugin of ours a pass ensures
// shares one source (localClaudePluginTarget), so the first with a source
// names it. The entry in the account's settings.json is rewritten through its
// symlink (planProfessorMarketplaceEntry); one in managed settings, which pfm
// never writes, fails the pass naming its file
// (managedProfessorMarketplaceConflict). A pass with no claude binary runs no
// add and edits nothing. A failure is appended to run and returned: the caller
// leaves every plugin of ours as it is, since no add of their marketplace
// could take.
func (installer *engine) ensureProfessorMarketplaceEntry(run *claudePluginRun, targets []ClaudePluginTarget) error {
	if run.resolveErr != nil {
		return nil
	}
	index := slices.IndexFunc(targets, func(target ClaudePluginTarget) bool {
		return target.Own && target.Source != ""
	})
	if index < 0 {
		return nil
	}
	target := &targets[index]
	err := managedProfessorMarketplaceConflict(installer.options.ManagedSettingsDir, target)
	if err == nil {
		var message string
		var edit func() error
		message, edit, err = planProfessorMarketplaceEntry(run.settingsPath, target)
		if err == nil && message != "" {
			err = installer.change(message, edit)
		}
	}
	if err != nil {
		err = installer.pluginStepFailure(run.dir, err)
		run.failures = append(run.failures, err)
	}
	return err
}

// planProfessorMarketplaceEntry plans the settings.json edit that points
// extraKnownMarketplaces.professor at target's source: a directory record of a
// local target's copies' dir, else a github record of its repository; the
// entry's other keys stay. It returns "" for an absent file or entry and for
// an entry already at that source (claudeMarketplaceSourceMatches): the add
// writes or keeps it itself. The edit writes through a settings.json symlink
// to the one file it resolves to, never replacing the link, and keeps every
// number exactly.
func planProfessorMarketplaceEntry(path string, target *ClaudePluginTarget) (string, func() error, error) {
	raw, document, err := readSettingsForEdit(path)
	if err != nil || document == nil {
		return "", nil, err
	}
	encoded, err := json.Marshal(document[extraKnownMarketplacesKey])
	if err != nil {
		return "", nil, fmt.Errorf("encode %s of %s: %w", extraKnownMarketplacesKey, path, err)
	}
	current, declared, err := professorMarketplaceEntry(encoded)
	if err != nil {
		return "", nil, fmt.Errorf("%s: %w", path, err)
	}
	if !declared || claudeMarketplaceSourceMatches(target.Source, current) {
		return "", nil, nil
	}
	pointProfessorMarketplaceEntry(document, target)
	named := EscapeControl(claudeMarketplaceSourceText(current))
	if named == "" {
		named = "no source"
	}
	message := fmt.Sprintf("point %s.%s in %s at %s: it names %s, and Claude Code refuses an add from another source",
		extraKnownMarketplacesKey, pfmconfig.ProfessorMarketplace, path, target.Source, named)
	return message, writeSettingsDocument(path, raw, document, func(fresh map[string]any) error {
		pointProfessorMarketplaceEntry(fresh, target)
		return nil
	}), nil
}

// professorMarketplaceEntry decodes an extraKnownMarketplaces value: the
// professor entry's source record and whether the value declares one. Null
// declares none; a value that is not an object of entries is an error naming
// the key.
func professorMarketplaceEntry(raw json.RawMessage) (claudeplugin.MarketplaceSource, bool, error) {
	var entries map[string]*struct {
		Source claudeplugin.MarketplaceSource `json:"source"`
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return claudeplugin.MarketplaceSource{}, false, fmt.Errorf("parse %s: %w", extraKnownMarketplacesKey, err)
	}
	entry := entries[pfmconfig.ProfessorMarketplace]
	if entry == nil {
		return claudeplugin.MarketplaceSource{}, false, nil
	}
	return entry.Source, true, nil
}

// marketplaceSourceKey is the key of a marketplace entry's source record, and of
// its kind inside that record.
const marketplaceSourceKey = "source"

// pointProfessorMarketplaceEntry sets the source of document's professor
// entry to target's, when document still declares that entry as an object.
func pointProfessorMarketplaceEntry(document map[string]any, target *ClaudePluginTarget) {
	entries, _ := document[extraKnownMarketplacesKey].(map[string]any)
	entry, _ := entries[pfmconfig.ProfessorMarketplace].(map[string]any)
	if entry == nil {
		return
	}
	source := map[string]any{marketplaceSourceKey: claudeplugin.MarketplaceGitHub, "repo": target.Source}
	if target.Local {
		source = map[string]any{marketplaceSourceKey: claudeplugin.MarketplaceDirectory, "path": target.Source}
	}
	entry[marketplaceSourceKey] = source
}

// managedProfessorMarketplaceConflict fails naming the managed settings file
// whose extraKnownMarketplaces.professor declares a source other than
// target's: Claude Code refuses that add too, and pfm never writes managed
// settings. dir is Options.ManagedSettingsDir, empty for none. A file or
// directory that cannot be read is an error naming it, never an absent entry.
func managedProfessorMarketplaceConflict(dir string, target *ClaudePluginTarget) error {
	if dir == "" {
		return nil
	}
	sources, err := managedSettingsSources(dir)
	if err != nil {
		return fmt.Errorf("list managed settings %s, so whether they declare the %s marketplace is unknown: %w",
			dir, pfmconfig.ProfessorMarketplace, err)
	}
	for _, path := range sources {
		document, _, err := readManagedSettings(path, os.Stat)
		if err != nil {
			return fmt.Errorf("read managed settings %s, so whether it declares the %s marketplace is unknown: %w",
				path, pfmconfig.ProfessorMarketplace, err)
		}
		value, present := document[extraKnownMarketplacesKey]
		if !present {
			continue
		}
		current, declared, err := professorMarketplaceEntry(value)
		if err != nil {
			return fmt.Errorf("managed settings %s: %w", path, err)
		}
		if declared && !claudeMarketplaceSourceMatches(target.Source, current) {
			return fmt.Errorf("%s.%s in managed settings %s points at %s, want %s: Claude Code refuses a %s "+
				"marketplace add from another source and pfm never writes managed settings — change or remove "+
				"that entry, then %s", extraKnownMarketplacesKey, pfmconfig.ProfessorMarketplace, path,
				EscapeControl(claudeMarketplaceSourceText(current)), target.Source, pfmconfig.ProfessorMarketplace,
				ClaudePluginRepair)
		}
	}
	return nil
}

// managedSettingsSources is every managed settings file Claude Code reads with
// dir, its managed-settings.d: each regular file or symlink named *.json in
// dir, not hidden, in name order, then the platform's managed-settings.json in
// the directory above. An absent dir lists that file alone; a dir that cannot
// be listed returns what it listed with the error.
func managedSettingsSources(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		err = nil
	}
	var sources []string
	for _, entry := range entries {
		name := entry.Name()
		listed := entry.Type().IsRegular() || entry.Type()&fs.ModeSymlink != 0
		if listed && !strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".json") {
			sources = append(sources, filepath.Join(dir, name))
		}
	}
	return append(sources, filepath.Join(filepath.Dir(dir), managedSettingsFile)), err
}
