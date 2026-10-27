package installer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/codexappendix"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
)

type extensionTarget struct {
	name, engine, path string
	fact               pfmconfig.ExtensionEngine
	home               string
}

func extensionTargets(options Options, ledger extensionOwnership) []extensionTarget {
	targets := map[string]extensionTarget{}
	for name := range options.Extensions {
		definition := pfmconfig.ExtensionRegistry()[name]
		dir := options.PrimaryConfigDir
		if dir == "" {
			dir = options.ConfigDir
		}
		if dir != "" {
			path := physicalSettingsPath(filepath.Join(dir, "settings.json"))
			targets[name+"/claude/"+path] = extensionTarget{
				name,
				extensionClaude,
				path,
				definition.Engines[extensionClaude],
				dir,
			}
		}
		for _, home := range options.CodexHomes {
			path := physicalSettingsPath(filepath.Join(home, "config.toml"))
			targets[name+"/codex/"+path] = extensionTarget{
				name,
				extensionCodex,
				path,
				definition.Engines[extensionCodex],
				home,
			}
		}
		if options.OpenCodeConfigPath != "" {
			path := physicalSettingsPath(options.OpenCodeConfigPath)
			targets[name+"/opencode/"+path] = extensionTarget{
				name,
				extensionOpenCode,
				path,
				definition.Engines[extensionOpenCode],
				options.Home,
			}
		}
	}
	for key := range ledger.Records {
		record := ledger.Records[key]
		targets[key] = extensionTarget{
			record.Name,
			record.Engine,
			record.Target,
			pfmconfig.ExtensionRegistry()[record.Name].Engines[record.Engine],
			record.Home,
		}
	}
	result := make([]extensionTarget, 0, len(targets))
	for _, key := range slices.Sorted(maps.Keys(targets)) {
		result = append(result, targets[key])
	}
	return result
}

func (target extensionTarget) key() string {
	return target.name + "/" + target.engine + "/" + target.path
}

// ensureExtensions runs before the existing Claude plugin step, so its ledger
// captures the pre-install keys rather than mistaking the CLI's writes for the user's.
func (installer *engine) ensureExtensions(ctx context.Context, uninstall bool) error {
	ledger, err := readExtensionOwnership(installer.managedRoot)
	if err != nil {
		return err
	}
	if len(installer.options.Extensions) == 0 && len(ledger.Records) == 0 {
		return nil
	}
	var failures []error
	targets := extensionTargets(installer.options, ledger)
	for index := range targets {
		target := &targets[index]
		record, has := ledger.Records[target.key()]
		if !has {
			record = extensionRecord{
				Name:   target.name,
				Engine: target.engine,
				Target: target.path,
				Home:   target.home,
				Values: map[string]extensionValue{},
			}
		}
		extension, configured := installer.options.Extensions[target.name]
		active := configured && extension.Active(target.engine) && !uninstall
		selected := configured && slices.Contains(extension.Engines, target.engine)
		if !has && (!selected || uninstall) {
			continue
		}
		if err := installer.convergeExtensionTarget(ctx, *target, extension, active, uninstall, &record); err != nil {
			ledger.Records[target.key()] = record
			failures = append(
				failures,
				fmt.Errorf("extension %s/%s %s: %w", target.name, target.engine, target.path, err),
			)
			continue
		}
		if uninstall {
			delete(ledger.Records, target.key())
		} else {
			ledger.Records[target.key()] = record
		}
	}
	saveErr := installer.change("record extension ownership", func() error {
		path := filepath.Join(installer.managedRoot, extensionOwnershipName)
		if len(ledger.Records) == 0 {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("remove extension ownership: %w", err)
			}
			return nil
		}
		raw, err := json.MarshalIndent(ledger, "", "  ")
		if err != nil {
			return fmt.Errorf("encode extension ownership: %w", err)
		}
		return atomicfile.Write(path, append(raw, '\n'), 0o600)
	})
	return errors.Join(append(failures, saveErr)...)
}

func (installer *engine) convergeExtensionTarget(
	ctx context.Context,
	target extensionTarget,
	extension pfmconfig.Extension,
	active, uninstall bool,
	record *extensionRecord,
) error {
	raw, before, err := readExtensionDocument(target.path, target.engine)
	if err != nil {
		return err
	}
	_, document, err := readExtensionDocument(target.path, target.engine)
	if err != nil {
		return err
	}
	switch {
	case uninstall:
		original := before
		// Decide restoration while the operator's current value is still present,
		// before the engine CLI can delete its enabledPlugins entry.
		var plannedEnablement any
		var plannedHasEnablement, protectEnablement bool
		enabledPath := []string{extensionEnabledPlugins, target.fact.ID}
		if target.engine == extensionClaude {
			_, protectEnablement = record.Values[strings.Join(enabledPath, "/")]
			if err := restoreExtensionValues(document, *record); err != nil {
				return err
			}
			plannedEnablement, plannedHasEnablement, err = extensionLookup(document, enabledPath)
			if err != nil {
				return err
			}
		}
		if err := installer.cleanupClaudeExtensionMarketplace(target, *record); err != nil {
			return err
		}
		if err := installer.uninstallExtensionPlugin(ctx, target, *record); err != nil {
			return err
		}
		raw, before, err = readExtensionDocument(target.path, target.engine)
		if err != nil {
			return err
		}
		_, document, err = readExtensionDocument(target.path, target.engine)
		if err != nil {
			return err
		}
		if protectEnablement {
			if err := extensionSet(document, enabledPath, plannedEnablement, !plannedHasEnablement); err != nil {
				return err
			}
		}

		for _, value := range record.Values {
			prior, has, err := extensionLookup(original, value.Path)
			if err != nil {
				return err
			}
			if has && extensionEqual(prior, value.Last) {
				_, still, err := extensionLookup(document, value.Path)
				if err != nil {
					return err
				}
				if !still && value.HadBefore {
					if err := extensionSet(document, value.Path, value.Last, false); err != nil {
						return err
					}
				}
			}
		}
		if err := restoreExtensionValues(document, *record); err != nil {
			return err
		}
	case target.engine == extensionOpenCode:
		if err := planOpenCodeExtension(document, target.fact.Package, active, record); err != nil {
			return err
		}
	default:
		enabledPath := []string{extensionEnabledPlugins, target.fact.ID}
		if target.engine == extensionCodex {
			enabledPath = []string{extensionPlugins, target.fact.ID, extensionEnabled}
		}
		_, owned := record.Values[strings.Join(enabledPath, "/")]
		if active && !owned {
			current, has, err := extensionLookup(document, enabledPath)
			if err != nil {
				return err
			}
			record.Values[strings.Join(enabledPath, "/")] = extensionValue{
				Path:      enabledPath,
				Before:    current,
				HadBefore: has,
				Last:      current,
			}
		}
		if active || owned || slices.Contains(extension.Engines, target.engine) {
			if err := ownExtensionValue(document, record, enabledPath, active); err != nil {
				return err
			}
		}
		if active && target.engine == extensionClaude {
			if err := restoreDroppedExtensionOptions(document, record, target.fact.ID, extension.Options); err != nil {
				return err
			}
			for _, key := range slices.Sorted(maps.Keys(extension.Options)) {
				if err := ownExtensionValue(
					document,
					record,
					[]string{extensionPluginConfigs, target.fact.ID, extensionOptions, key},
					extension.Options[key],
				); err != nil {
					return err
				}
			}
		}
	}
	if uninstall && target.engine == extensionOpenCode {
		if err := planOpenCodeExtension(document, target.fact.Package, record.PackageBefore, record); err != nil {
			return err
		}
		if !record.PluginArrayBefore {
			if entries, _ := document["plugin"].([]any); len(entries) == 0 {
				delete(document, "plugin")
			}
		}
	}
	record.Env = nil
	if active {
		record.Env = maps.Clone(extension.Env)
	}
	if active && target.engine != extensionOpenCode {
		if err := installer.captureExtensionInstall(target, record); err != nil {
			return err
		}
	}
	wanted, err := encodeExtensionEdit(raw, target.engine, before, document, *record)
	if err != nil {
		return err
	}
	if err := installer.writeExtensionDocument(target.path, raw, wanted); err != nil {
		return err
	}
	if active && target.engine == extensionCodex {
		if err := installer.installCodexExtension(ctx, target); err != nil {
			return err
		}
		if installer.apply {
			if err := captureExtensionCLIChanges(target, before, record); err != nil {
				return err
			}
		}
		if installer.apply {
			binary := installer.options.CodexBinary
			if binary == "" {
				binary = extensionCodex
			}
			if err := codexappendix.RegisterPluginHookTrusts(ctx, binary, target.home, target.fact.ID); err != nil {
				return err
			}
			if err := captureExtensionCLIChanges(target, before, record); err != nil {
				return err
			}
		} else {
			installer.say("  change  trust hooks of extension %s in %s", target.fact.ID, target.home)
		}
	}
	return nil
}

// OpenCode 1.x has no disable bit: own exact package membership, remembering
// its original presence for uninstall, and keep every foreign entry.
func planOpenCodeExtension(document map[string]any, pkg string, active bool, record *extensionRecord) error {
	entries, err := extensionPackageEntries(document)
	if err != nil {
		return err
	}
	found := slices.Contains(entries, any(pkg))
	if !record.PackageAdded {
		record.PackageBefore = found
		_, record.PluginArrayBefore = document["plugin"]
		record.PackageAdded = true
	}
	if active {
		if !found {
			entries = append(slices.Clone(entries), pkg)
		}
	}
	if !active && record.PackageAdded {
		entries = slices.DeleteFunc(slices.Clone(entries), func(value any) bool { return value == pkg })
	}
	if len(entries) > 0 || document["plugin"] != nil {
		document["plugin"] = entries
	}
	return nil
}

func (installer *engine) installCodexExtension(ctx context.Context, target extensionTarget) error {
	home := target.home
	installed, err := extensionCacheInstalled(home, target.fact)
	if err != nil {
		return err
	}
	_, document, err := readExtensionDocument(target.path, extensionCodex)
	if err != nil {
		return err
	}
	_, marketplace, err := extensionLookup(document, []string{extensionMarketplaces, target.fact.Marketplace})
	if err != nil {
		return err
	}
	if installed && marketplace {
		return nil
	}
	binary := installer.options.CodexBinary
	if binary == "" {
		binary = extensionCodex
	}
	return installer.change("install extension "+target.fact.ID+" in "+home, func() error {
		environment := deps.EnvironmentWith("CODEX_HOME", home)
		for _, args := range [][]string{
			append(slices.Clone(target.fact.MarketplaceArgs), target.fact.Source),
			append(append(slices.Clone(target.fact.InstallArgs), target.fact.ID), target.fact.InstallSuffix...),
		} {
			if installed && len(args) > 1 && args[1] == extensionAdd {
				continue
			}
			argv := append([]string{binary}, args...)
			if err := runClaudePluginCommand(
				ctx,
				installer.processRunner(),
				argv,
				deps.RunOptions{Env: environment},
			); err != nil {
				return err
			}
		}
		installed, err := extensionCacheInstalled(home, target.fact)
		if err != nil {
			return err
		}
		if !installed {
			return fmt.Errorf(
				"extension %s install returned success without a plugin cache in %s",
				target.fact.ID,
				home,
			)
		}
		return nil
	})
}

func extensionCacheInstalled(home string, fact pfmconfig.ExtensionEngine) (bool, error) {
	path := filepath.Join(append([]string{home}, fact.CachePath...)...)
	if len(fact.CachePath) == 0 {
		return false, errors.New("extension registry has no cache path")
	}
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read extension cache %s: %w", path, err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return true, nil
		}
	}
	return false, nil
}
