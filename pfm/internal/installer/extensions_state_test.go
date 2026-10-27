package installer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestExtensionOwnershipRestoreHandEdits(t *testing.T) {
	doc := map[string]any{"options": map[string]any{"foreign": "keep", "managed": "before"}}
	record := extensionRecord{Values: map[string]extensionValue{}}
	if err := ownExtensionValue(doc, &record, []string{"options", "managed"}, "after"); err != nil {
		t.Fatal(err)
	}
	if err := ownExtensionValue(doc, &record, []string{"options", "added"}, false); err != nil {
		t.Fatal(err)
	}
	if err := extensionSet(doc, []string{"options", "added"}, "hand edit", false); err != nil {
		t.Fatal(err)
	}
	if err := restoreExtensionValues(doc, record); err != nil {
		t.Fatal(err)
	}
	if value, _, _ := extensionLookup(doc, []string{"options", "managed"}); value != "before" {
		t.Fatalf("original not restored: %v", doc)
	}
	if value, _, _ := extensionLookup(doc, []string{"options", "added"}); value != "hand edit" {
		t.Fatalf("hand edit lost: %v", doc)
	}
	if value, _, _ := extensionLookup(doc, []string{"options", "foreign"}); value != "keep" {
		t.Fatalf("foreign key lost: %v", doc)
	}
}

func TestExtensionTOMLEditPreservesForeignBytes(t *testing.T) {
	raw := []byte(
		"# keep header\nmodel='fixture'\n[plugins.\"ponytail@ponytail\"]\n# keep plugin comment\nenabled=false\nforeign='keep'\n[plugins.other]\nenabled=true\n",
	)
	var before, after map[string]any
	if _, err := toml.Decode(string(raw), &before); err != nil {
		t.Fatal(err)
	}
	if _, err := toml.Decode(string(raw), &after); err != nil {
		t.Fatal(err)
	}
	record := extensionRecord{Values: map[string]extensionValue{}}
	if err := ownExtensionValue(after, &record, []string{"plugins", "ponytail@ponytail", "enabled"}, true); err != nil {
		t.Fatal(err)
	}
	wanted, err := encodeExtensionEdit(raw, "codex", before, after, record)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"# keep header", "# keep plugin comment", "foreign='keep'", "[plugins.other]\nenabled=true"} {
		if !strings.Contains(string(wanted), text) {
			t.Fatalf("foreign text lost %q: %s", text, wanted)
		}
	}
	var checked map[string]any
	if _, err := toml.Decode(string(wanted), &checked); err != nil {
		t.Fatal(err)
	}
	value, _, err := extensionLookup(checked, []string{"plugins", "ponytail@ponytail", "enabled"})
	if err != nil || value != true {
		t.Fatalf("enabled not converged: %s (%v)", wanted, err)
	}
}

func TestExtensionOwnershipPreservesLargeNumbers(t *testing.T) {
	root := t.TempDir()
	const large = "9007199254740993"
	writeFixture(
		t,
		filepath.Join(root, extensionOwnershipName),
		`{"version":1,"records":{"fixture":{"values":{"options/id":{"path":["options","id"],"before":`+large+`,"hadBefore":true,"last":1}}}}}`,
	)
	ledger, err := readExtensionOwnership(root)
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{"options": map[string]any{"id": json.Number("1")}}
	if err := restoreExtensionValues(doc, ledger.Records["fixture"]); err != nil {
		t.Fatal(err)
	}
	value, _, err := extensionLookup(doc, []string{"options", "id"})
	if err != nil || value != json.Number(large) {
		t.Fatalf("original number changed: %v (%T), err=%v", value, value, err)
	}
}

func TestExtensionTOMLEditKeepsDottedKeys(t *testing.T) {
	for _, raw := range []string{
		"plugins.\"ponytail@ponytail\".enabled = false\nforeign = 'keep'\n",
		"[plugins]\n\"ponytail@ponytail\".enabled = false\nother.enabled = true\n",
	} {
		var before, after map[string]any
		if _, err := toml.Decode(raw, &before); err != nil {
			t.Fatal(err)
		}
		if _, err := toml.Decode(raw, &after); err != nil {
			t.Fatal(err)
		}
		record := extensionRecord{Values: map[string]extensionValue{}}
		if err := ownExtensionValue(
			after,
			&record,
			[]string{"plugins", "ponytail@ponytail", "enabled"},
			true,
		); err != nil {
			t.Fatal(err)
		}
		wanted, err := encodeExtensionEdit([]byte(raw), extensionCodex, before, after, record)
		if err != nil {
			t.Fatal(err)
		}
		var actual map[string]any
		if _, err := toml.Decode(string(wanted), &actual); err != nil {
			t.Fatal(err)
		}
		if !extensionEqual(actual, after) {
			t.Fatalf("dotted key changed meaning: %s", wanted)
		}
	}
}

func TestInstallerLockSerializesOwnershipAcrossRootRetirement(t *testing.T) {
	root := filepath.Join(t.TempDir(), "managed")
	unlock, err := lockInstaller(root)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := lockInstaller(root); err == nil {
		_ = second()
		t.Fatal("concurrent installer acquired ownership lock")
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if second, err := lockInstaller(root); err == nil {
		_ = second()
		t.Fatal("root retirement replaced the lock inode")
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	release, err := lockInstaller(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}
