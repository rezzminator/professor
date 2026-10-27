package installer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

func claudeExtensionMarketplacePath(target extensionTarget) string {
	return filepath.Join(target.home, "plugins", "known_marketplaces.json")
}

// Capture only the marketplace entry that the existing Claude plugin step wrote.
func (installer *engine) recordClaudeExtensionMarketplaces() error {
	if !installer.apply {
		return nil
	}
	ledger, err := readExtensionOwnership(installer.managedRoot)
	if err != nil {
		return err
	}
	for key := range ledger.Records {
		record := ledger.Records[key]
		if record.Engine != extensionClaude || !record.MarketplaceCaptured || record.MarketplaceBefore != nil ||
			record.MarketplaceLast != nil {
			continue
		}
		target := extensionTarget{home: record.Home}
		target.fact = pfmconfig.ExtensionRegistry()[record.Name].Engines[extensionClaude]
		_, document, err := readExtensionDocument(claudeExtensionMarketplacePath(target), extensionClaude)
		if err != nil {
			return err
		}
		record.MarketplaceLast = document[target.fact.Marketplace]
		ledger.Records[key] = record
	}
	raw, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return fmt.Errorf("encode extension marketplace ownership: %w", err)
	}
	if len(ledger.Records) == 0 {
		return nil
	}
	return atomicfile.Write(filepath.Join(installer.managedRoot, extensionOwnershipName), append(raw, '\n'), 0o600)
}

func (installer *engine) cleanupClaudeExtensionMarketplace(target extensionTarget, record extensionRecord) error {
	if target.engine != extensionClaude || record.MarketplaceLast == nil {
		return nil
	}
	path := claudeExtensionMarketplacePath(target)
	raw, document, err := readExtensionDocument(path, extensionClaude)
	if err != nil {
		return err
	}
	current := document[target.fact.Marketplace]
	if !extensionEqual(current, record.MarketplaceLast) {
		return nil
	}
	delete(document, target.fact.Marketplace)
	wanted, err := encodeSettingsDocument(document)
	if err != nil {
		return err
	}
	if err := installer.writeExtensionDocument(path, raw, wanted); err != nil {
		return err
	}
	entry, _ := current.(map[string]any)
	location, _ := entry["installLocation"].(string)
	owned := filepath.Join(target.home, "plugins", "marketplaces", target.fact.Marketplace)
	if location != "" && filepath.Clean(location) == owned {
		return installer.change("remove owned extension marketplace "+owned, func() error {
			if err := os.RemoveAll(owned); err != nil {
				return fmt.Errorf("remove extension marketplace %s: %w", owned, err)
			}
			return nil
		})
	}
	return nil
}

func restoreDroppedExtensionOptions(
	document map[string]any,
	record *extensionRecord,
	id string,
	want map[string]any,
) error {
	for key, value := range record.Values {
		if len(value.Path) != 4 || value.Path[0] != extensionPluginConfigs || value.Path[1] != id ||
			value.Path[2] != extensionOptions {
			continue
		}
		if _, has := want[value.Path[3]]; has {
			continue
		}
		if err := restoreExtensionValues(
			document,
			extensionRecord{Values: map[string]extensionValue{key: value}},
		); err != nil {
			return err
		}
		delete(record.Values, key)
	}
	// Parent objects remain owned until uninstall; only remove empty objects there.
	return nil
}
