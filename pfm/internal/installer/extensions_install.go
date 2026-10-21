package installer

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

func (installer *engine) captureExtensionInstall(target extensionTarget, record *extensionRecord) error {
	installed, err := extensionInstalled(target)
	if err != nil {
		return err
	}
	if !installed {
		record.InstalledByPFM = true
	}
	if target.engine == extensionClaude && !record.MarketplaceCaptured {
		_, doc, err := readExtensionDocument(claudeExtensionMarketplacePath(target), extensionClaude)
		if err != nil {
			return err
		}
		record.MarketplaceBefore = doc[target.fact.Marketplace]
		record.MarketplaceCaptured = true
	}
	if target.engine == extensionCodex {
		_, document, err := readExtensionDocument(target.path, target.engine)
		if err != nil {
			return err
		}
		_, has, err := extensionLookup(document, []string{extensionMarketplaces, target.fact.Marketplace})
		if err != nil {
			return err
		}
		if !has {
			record.MarketplaceAdded = true
		}
	}
	return nil
}

func extensionInstalled(target extensionTarget) (bool, error) {
	if target.engine == extensionCodex {
		return extensionCacheInstalled(target.home, target.fact)
	}
	missing, err := ClaudePluginsNotInstalled(target.home, []ClaudePluginTarget{{ID: target.fact.ID}})
	return len(missing) == 0, err
}

// captureExtensionCLIChanges records only the exact plugin's scalar config
// writes. No neighboring plugin, marketplace or native hook can be claimed.
func captureExtensionCLIChanges(target extensionTarget, before map[string]any, record *extensionRecord) error {
	_, after, err := readExtensionDocument(target.path, target.engine)
	if err != nil {
		return err
	}
	roots := [][]string{{extensionPlugins, target.fact.ID}, {extensionMarketplaces, target.fact.Marketplace}}
	hooks, _, err := extensionLookup(after, []string{extensionHooks, extensionHookState})
	if err != nil {
		return err
	}
	if state, ok := hooks.(map[string]any); ok {
		for key := range state {
			if strings.HasPrefix(key, target.fact.ID+":") {
				roots = append(roots, []string{extensionHooks, extensionHookState, key})
			}
		}
	}
	for _, root := range roots {
		value, has, err := extensionLookup(after, root)
		if err != nil {
			return err
		}
		if !has {
			continue
		}
		if err := captureExtensionLeaves(before, root, value, record); err != nil {
			return err
		}
	}
	return nil
}

func captureExtensionLeaves(before map[string]any, path []string, value any, record *extensionRecord) error {
	if object, ok := value.(map[string]any); ok {
		for _, key := range slices.Sorted(maps.Keys(object)) {
			if err := captureExtensionLeaves(before, append(slices.Clone(path), key), object[key], record); err != nil {
				return err
			}
		}
		return nil
	}
	prior, has, err := extensionLookup(before, path)
	if err != nil {
		return err
	}
	key := strings.Join(path, "/")
	if has && extensionEqual(prior, value) {
		return nil
	}
	if owned, ok := record.Values[key]; ok {
		owned.Last = value
		record.Values[key] = owned
		return nil
	}
	rememberExtensionTables(before, path, record)
	record.Values[key] = extensionValue{Path: path, Before: prior, HadBefore: has, Last: value}
	return nil
}

func (installer *engine) uninstallExtensionPlugin(
	ctx context.Context,
	target extensionTarget,
	record extensionRecord,
) error {
	if target.engine == extensionOpenCode || !record.InstalledByPFM {
		return nil
	}
	installed, err := extensionInstalled(target)
	if err != nil {
		return err
	}
	if !installed {
		return nil
	}
	binary := installer.options.CodexBinary
	environment := deps.EnvironmentWith("CODEX_HOME", target.home)
	if target.engine == extensionClaude {
		binary, err = ResolveClaudeBinary(
			installer.options.Home,
			installer.options.ClaudeBinary,
			installer.env().Get("PATH"),
		)
		if err != nil {
			return err
		}
		environment = pluginCommandEnvironment(target.home)
	}
	if binary == "" {
		binary = target.engine
	}
	argv := append([]string{binary}, target.fact.UninstallArgs...)
	argv = append(argv, target.fact.ID)
	argv = append(argv, target.fact.UninstallSuffix...)
	return installer.change("uninstall owned extension "+target.fact.ID+" in "+target.home, func() error {
		if err := runClaudePluginCommand(
			ctx,
			installer.processRunner(),
			argv,
			deps.RunOptions{Env: environment},
		); err != nil {
			return fmt.Errorf("remove owned extension %s: %w", target.fact.ID, err)
		}
		return nil
	})
}
