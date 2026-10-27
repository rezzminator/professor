package installer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

func TestExtensionMarketplaceOwnershipAndOptionRemoval(t *testing.T) {
	inst := extensionFixture(t)
	if err := inst.ensureExtensions(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	fact := pfmconfig.ExtensionRegistry()["ponytail"].Engines["claude"]
	target := extensionTarget{name: "ponytail", engine: "claude", home: inst.options.PrimaryConfigDir, fact: fact}
	path := claudeExtensionMarketplacePath(target)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		path,
		[]byte(`{"ponytail":{"source":"fixture"},"foreign":{"source":"keep"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := inst.recordClaudeExtensionMarketplaces(); err != nil {
		t.Fatal(err)
	}
	ledger, err := readExtensionOwnership(inst.managedRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range ledger.Records {
		if record.Engine == "claude" {
			if err := inst.cleanupClaudeExtensionMarketplace(target, record); err != nil {
				t.Fatal(err)
			}
		}
	}
	_, doc, err := readExtensionDocument(path, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if doc["ponytail"] != nil || doc["foreign"] == nil {
		t.Fatalf("marketplace cleanup lost ownership: %v", doc)
	}
	document := map[string]any{}
	record := extensionRecord{Values: map[string]extensionValue{}}
	option := []string{"pluginConfigs", fact.ID, "options", "ledger"}
	if err := ownExtensionValue(document, &record, option, false); err != nil {
		t.Fatal(err)
	}
	if err := restoreDroppedExtensionOptions(document, &record, fact.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, has, err := extensionLookup(document, option); err != nil || has {
		t.Fatalf("removed option remains: %v err=%v", document, err)
	}
}
