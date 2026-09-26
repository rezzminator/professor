package installer

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestVSCodeExtensionIsRegisteredInEachProductsIndexNotOnlyLinked pins issue
// #24 9a: a symlink under extensions/ is never loaded on its own — modern VS
// Code reads its user extension list from extensions.json, and a product
// whose index predated the link never scans the directory it names. Two
// product roots cover both starting shapes: one with an existing
// extensions.json (an empty array, so no other entry survives to check), one
// with none at all; a THIRD root's index also carries a foreign
// pre-installed extension that must survive byte-for-byte in meaning after
// the JSON re-encode.
func TestVSCodeExtensionIsRegisteredInEachProductsIndexNotOnlyLinked(t *testing.T) {
	t.Setenv("VSCODE_PORTABLE", "")
	home := t.TempDir()
	rootEmptyArray := filepath.Join(home, "product-empty-array")
	rootNoIndex := filepath.Join(home, "product-no-index")
	rootForeign := filepath.Join(home, "product-foreign")
	for _, root := range []string{rootEmptyArray, rootNoIndex, rootForeign} {
		if err := os.MkdirAll(filepath.Join(root, "extensions"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(
		filepath.Join(rootEmptyArray, "extensions", "extensions.json"),
		[]byte("[]"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	foreignEntry := `[{"identifier":{"id":"foo.bar"},"version":"1.2.3","relativeLocation":"foo.bar-1.2.3","location":{"$mid":1,"path":"/opt/foo/extensions/foo.bar-1.2.3","scheme":"file"}}]`
	foreignIndexPath := filepath.Join(rootForeign, "extensions", "extensions.json")
	if err := os.WriteFile(foreignIndexPath, []byte(foreignEntry), 0o644); err != nil {
		t.Fatal(err)
	}

	raw, err := embeddedAssets.ReadFile("assets/" + vscodeExtensionSource + "/package.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}

	installer := newVSCodeExtensionEngine(home, []string{rootEmptyArray, rootNoIndex, rootForeign}, true)
	if err := installer.wireVSCode(); err != nil {
		t.Fatal(err)
	}

	for _, root := range []string{rootEmptyArray, rootNoIndex} {
		indexPath := filepath.Join(root, "extensions", "extensions.json")
		raw, err := os.ReadFile(indexPath)
		if err != nil {
			t.Fatalf("read %s: %v", indexPath, err)
		}
		var entries []map[string]any
		if err := json.Unmarshal(raw, &entries); err != nil {
			t.Fatalf("decode %s: %v", indexPath, err)
		}
		if len(entries) != 1 {
			t.Fatalf("%s entries = %v, want exactly one", indexPath, entries)
		}
		identifier, _ := entries[0]["identifier"].(map[string]any)
		if id, _ := identifier["id"].(string); id != "professor.professor" {
			t.Fatalf("%s identifier.id = %v, want professor.professor", indexPath, entries[0]["identifier"])
		}
		if relativeLocation, _ := entries[0]["relativeLocation"].(string); relativeLocation != "professor" {
			t.Fatalf("%s relativeLocation = %v, want professor", indexPath, entries[0]["relativeLocation"])
		}
		if version, _ := entries[0]["version"].(string); version != manifest.Version {
			t.Fatalf("%s version = %v, want %s", indexPath, entries[0]["version"], manifest.Version)
		}
	}

	foreignRaw, err := os.ReadFile(foreignIndexPath)
	if err != nil {
		t.Fatal(err)
	}
	var foreignEntries []map[string]any
	if err := json.Unmarshal(foreignRaw, &foreignEntries); err != nil {
		t.Fatal(err)
	}
	if len(foreignEntries) != 2 {
		t.Fatalf("%s entries = %v, want the foreign entry plus professor.professor", foreignIndexPath, foreignEntries)
	}
	var foreignSurvived, professorPresent bool
	for _, entry := range foreignEntries {
		identifier, _ := entry["identifier"].(map[string]any)
		id, _ := identifier["id"].(string)
		switch id {
		case "foo.bar":
			relativeLocation, _ := entry["relativeLocation"].(string)
			location, _ := entry["location"].(map[string]any)
			path, _ := location["path"].(string)
			if relativeLocation == "foo.bar-1.2.3" && path == "/opt/foo/extensions/foo.bar-1.2.3" {
				foreignSurvived = true
			}
		case "professor.professor":
			professorPresent = true
		}
	}
	if !foreignSurvived {
		t.Fatalf("foreign pre-existing entry did not survive: %v", foreignEntries)
	}
	if !professorPresent {
		t.Fatalf("professor.professor was not registered alongside the foreign entry: %v", foreignEntries)
	}
}

// TestVSCodeExtensionIndexUnreadableIsSkippedVisiblyAndTheLinkStillMade pins
// the "an unreadable index is a visible skip, never absent" rule: a
// malformed extensions.json is never rewritten (pfm cannot know what it
// would be destroying), the skip line names the index, and the symlink
// itself — which does not require parsing anything — is still made.
func TestVSCodeExtensionIndexUnreadableIsSkippedVisiblyAndTheLinkStillMade(t *testing.T) {
	t.Setenv("VSCODE_PORTABLE", "")
	home := t.TempDir()
	root := filepath.Join(home, "product-a")
	if err := os.MkdirAll(filepath.Join(root, "extensions"), 0o755); err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(root, "extensions", "extensions.json")
	malformed := "{not json"
	if err := os.WriteFile(indexPath, []byte(malformed), 0o644); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	installer := newVSCodeExtensionEngine(home, []string{root}, true)
	installer.options.Stdout = &output
	if err := installer.wireVSCode(); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(output.String(), "VS Code extension index "+indexPath+" unreadable") {
		t.Fatalf("did not name the unreadable index:\n%s", output.String())
	}
	got, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != malformed {
		t.Fatalf("unreadable index was rewritten: %s", got)
	}
	source := filepath.Join(installer.managedRoot, filepath.FromSlash(vscodeExtensionSource))
	target := filepath.Join(root, "extensions", vscodeExtensionLinkName)
	resolved, linked := resolvedLink(target)
	if !linked || resolved != filepath.Clean(source) {
		t.Fatalf("link was not made despite the unreadable index: resolved=%q linked=%v", resolved, linked)
	}
}

// TestVSCodeExtensionUninstallRemovesOnlyItsOwnIndexEntry pins the removal
// predicate in unregisterVSCodeExtension: relativeLocation=="professor" is
// what "ours" means (the fixed folder name pfm links), so a foreign entry
// sharing nothing but the same index file is never touched.
func TestVSCodeExtensionUninstallRemovesOnlyItsOwnIndexEntry(t *testing.T) {
	t.Setenv("VSCODE_PORTABLE", "")
	home := t.TempDir()
	root := filepath.Join(home, "product-a")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	roots := []string{root}

	installer := newVSCodeExtensionEngine(home, roots, true)
	if err := installer.wireVSCode(); err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(root, "extensions", "extensions.json")
	raw, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	var entries []map[string]any
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatal(err)
	}
	entries = append(entries, map[string]any{
		"identifier":       map[string]any{"id": "foo.bar"},
		"version":          "1.2.3",
		"relativeLocation": "foo.bar-1.2.3",
		"location": map[string]any{
			"$mid":   float64(1),
			"path":   "/opt/foo/extensions/foo.bar-1.2.3",
			"scheme": "file",
		},
	})
	encoded, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexPath, encoded, 0o644); err != nil {
		t.Fatal(err)
	}

	uninstaller := newVSCodeExtensionEngine(home, roots, false)
	uninstaller.options.Mode = ModeUninstall
	if err := uninstaller.wireVSCode(); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	var after []map[string]any
	if err := json.Unmarshal(got, &after); err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 {
		t.Fatalf("index entries after uninstall = %v, want only the foreign entry", after)
	}
	identifier, _ := after[0]["identifier"].(map[string]any)
	if id, _ := identifier["id"].(string); id != "foo.bar" {
		t.Fatalf("uninstall removed the wrong entry: %v", after)
	}
}

// TestInspectVSCodeSurvivesOneUnreadableSettingsFileAndReportsEveryOtherRow
// is a REGRESSION test for issue #24 F6: InspectVSCode used to abort with a
// bare error the instant ONE owned settings file returned a non-ENOENT read
// error, discarding every already-classified product and settings row —
// a BROKEN link on another product would go unreported behind it. Unfixed,
// this returns (VSCodeReport{}, non-nil error) instead of a report carrying
// both rows.
func TestInspectVSCodeSurvivesOneUnreadableSettingsFileAndReportsEveryOtherRow(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}
	t.Setenv("VSCODE_PORTABLE", "")
	home := t.TempDir()

	readablePath := filepath.Join(home, "readable-product", "settings.json")
	unreadablePath := filepath.Join(home, "unreadable-product", "settings.json")
	for _, path := range []string{readablePath, unreadablePath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		// Each file carries the owned profile: the readable row must classify
		// as "owned", so the assertion tests survival, not a missing profile.
		if err := os.WriteFile(
			path,
			[]byte(`{"terminal.integrated.profiles.linux":{"PFM":{"path":"/bin/zsh"}}}`),
			0o644,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(unreadablePath, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(unreadablePath, 0o644); err != nil {
			t.Errorf("restore unreadable settings permissions: %v", err)
		}
	})

	installer := newVSCodeExtensionEngine(home, nil, true)
	ownership := map[string]vscodeOwnershipRecord{
		readablePath:   {Path: readablePath, Platform: "linux", ProfileOwned: true},
		unreadablePath: {Path: unreadablePath, Platform: "linux", ProfileOwned: true},
	}
	ownershipPath := filepath.Join(installer.managedRoot, vscodeOwnershipName)
	if err := installer.writeVSCodeOwnership(ownershipPath, nil, ownership, nil, nil); err != nil {
		t.Fatal(err)
	}

	report, err := InspectVSCode(home)
	if err != nil {
		t.Fatalf("InspectVSCode(%q) returned an error instead of a per-row unreadable status: %v", home, err)
	}
	if len(report.Settings) != 2 {
		t.Fatalf(
			"report.Settings has %d rows, want 2 (the readable row must survive the unreadable one): %+v",
			len(report.Settings),
			report.Settings,
		)
	}
	var sawReadable, sawUnreadable bool
	for _, status := range report.Settings {
		switch status.Path {
		case readablePath:
			sawReadable = true
			if status.Profile != "owned" {
				t.Fatalf("readable settings row Profile=%q, want %q", status.Profile, "owned")
			}
		case unreadablePath:
			sawUnreadable = true
			if status.Profile != "unreadable" {
				t.Fatalf("unreadable settings row Profile=%q, want %q", status.Profile, "unreadable")
			}
			if status.Error == "" {
				t.Fatal(
					"unreadable settings row carries no Error text — a read error must never render as bare absence",
				)
			}
		}
	}
	if !sawReadable || !sawUnreadable {
		t.Fatalf(
			"report.Settings missing a row: sawReadable=%v sawUnreadable=%v: %+v",
			sawReadable,
			sawUnreadable,
			report.Settings,
		)
	}
}
