package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"hostops/pfm/internal/atomicfile"
)

// vscodeExtensionManifestInfo is the two fields registerVSCodeExtension and
// InspectVSCode need out of the staged package.json: the extension id VS
// Code's own index keys on (publisher.name — vscode_extension_test.go:476
// already pins this contract) and the version pfm claims in a fresh entry.
type vscodeExtensionManifestInfo struct {
	ID      string
	Version string
}

// vscodeExtensionManifest reads the embedded package.json once — the file
// never changes within a process, and every product's index write reads the
// same two fields.
var vscodeExtensionManifest = sync.OnceValues(func() (vscodeExtensionManifestInfo, error) {
	raw, err := embeddedAssets.ReadFile("assets/" + vscodeExtensionSource + "/package.json")
	if err != nil {
		return vscodeExtensionManifestInfo{}, fmt.Errorf("read embedded VS Code extension package.json: %w", err)
	}
	var manifest struct {
		Name      string `json:"name"`
		Publisher string `json:"publisher"`
		Version   string `json:"version"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return vscodeExtensionManifestInfo{}, fmt.Errorf("decode embedded VS Code extension package.json: %w", err)
	}
	if manifest.Publisher == "" || manifest.Name == "" || manifest.Version == "" {
		return vscodeExtensionManifestInfo{}, fmt.Errorf(
			"embedded VS Code extension package.json missing publisher/name/version",
		)
	}
	return vscodeExtensionManifestInfo{ID: manifest.Publisher + "." + manifest.Name, Version: manifest.Version}, nil
})

// readVSCodeExtensionIndex reads and parses <extensions dir>/extensions.json
// — the file modern VS Code actually scans user extensions from
// (AbstractExtensionsScannerService.scanUserExtensions), never the directory
// walk a hand-linked folder relies on. Absence is the empty index (a product
// that has never installed anything through its own UI yet); a parse
// failure is returned to the caller rather than guessed at, so it can be
// reported as a visible skip rather than silently treated as empty.
func readVSCodeExtensionIndex(path string) ([]map[string]any, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return []map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	var entries []map[string]any
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// vscodeIndexFind returns the first entry whose identifier.id matches id.
func vscodeIndexFind(entries []map[string]any, id string) (map[string]any, bool) {
	for _, entry := range entries {
		identifier, _ := entry["identifier"].(map[string]any)
		if entryID, _ := identifier["id"].(string); entryID == id {
			return entry, true
		}
	}
	return nil, false
}

// vscodeIndexEntryOwned reads the fields registerVSCodeExtension needs to
// decide ownership of an existing entry: whether it names OUR link
// (relativeLocation == vscodeExtensionLinkName, location.path == target).
func vscodeIndexEntryOwned(entry map[string]any, target string) bool {
	relativeLocation, _ := entry["relativeLocation"].(string)
	if relativeLocation != vscodeExtensionLinkName {
		return false
	}
	location, _ := entry["location"].(map[string]any)
	entryPath, _ := location["path"].(string)
	return entryPath == target
}

func vscodeIndexEntryOtherLocation(entry map[string]any) string {
	location, _ := entry["location"].(map[string]any)
	if path, _ := location["path"].(string); path != "" {
		return path
	}
	if relativeLocation, _ := entry["relativeLocation"].(string); relativeLocation != "" {
		return relativeLocation
	}
	return "(unknown location)"
}

// registerVSCodeExtension is linkVSCodeExtension's second half: a symlink
// under extensionsDir is never loaded on its own — modern VS Code reads its
// user extension list from extensionsDir/extensions.json
// (IStoredProfileExtension records), and walks the directory only when that
// file does not exist yet or a folder appears while the window is already
// running. This writes (or updates) the professor.professor entry there, so
// the product loads the link on its next start, the same guarantee a fresh
// `code --install-extension` gives a .vsix.
//
// An index this cannot parse is reported as a visible skip and left
// untouched — never rewritten guessing at its shape, per the "an unreadable
// index is a visible skip, never rendered as absent" rule. An entry that
// already names professor.professor from a DIFFERENT relativeLocation/path
// (a marketplace install, say) is left alone too; pfm only ever owns the
// entry it wrote itself.
func (installer *engine) registerVSCodeExtension(extensionsDir string) (bool, error) {
	manifest, err := vscodeExtensionManifest()
	if err != nil {
		return false, err
	}
	target := filepath.Join(extensionsDir, vscodeExtensionLinkName)
	indexPath := filepath.Join(extensionsDir, vscodeExtensionIndexName)
	entries, err := readVSCodeExtensionIndex(indexPath)
	if err != nil {
		installer.skip(
			"VS Code extension index " + indexPath + " unreadable: " + err.Error() + " — the product will not load the link until the index is repaired",
		)
		return false, nil
	}

	existing, found := vscodeIndexFind(entries, manifest.ID)
	if found && !vscodeIndexEntryOwned(existing, target) {
		installer.skip(
			"VS Code extension index " + indexPath + " already registers " + manifest.ID + " from " + vscodeIndexEntryOtherLocation(
				existing,
			),
		)
		return false, nil
	}
	if found {
		if version, _ := existing["version"].(string); version == manifest.Version {
			installer.ok(indexPath)
			return false, nil
		}
	}

	entry := map[string]any{
		"identifier": map[string]any{"id": manifest.ID},
		"version":    manifest.Version,
		"location": map[string]any{
			"$mid":        1,
			vscodePathKey: target,
			"scheme":      "file",
		},
		"relativeLocation": vscodeExtensionLinkName,
		"metadata": map[string]any{
			"installedTimestamp": installer.now().UnixMilli(),
			"pinned":             false,
			"source":             MCPClientPFM,
		},
	}
	updated := make([]map[string]any, len(entries))
	copy(updated, entries)
	if found {
		for index, candidate := range updated {
			identifier, _ := candidate["identifier"].(map[string]any)
			if entryID, _ := identifier["id"].(string); entryID == manifest.ID {
				updated[index] = entry
				break
			}
		}
	} else {
		updated = append(updated, entry)
	}
	encoded, err := json.MarshalIndent(updated, "", "  ")
	if err != nil {
		return false, err
	}
	encoded = append(encoded, '\n')

	description := "register " + manifest.ID + " in " + indexPath + " (product loads it on next start)"
	return true, installer.change(description, func() error {
		if err := atomicfile.Write(indexPath, encoded, 0o644); err != nil {
			return err
		}
		// Verification is the product's OWN view: re-read the index and
		// assert the entry is present — the install never claims more than
		// the index itself proves.
		verifyEntries, verifyErr := readVSCodeExtensionIndex(indexPath)
		if verifyErr != nil {
			return fmt.Errorf("verify VS Code extension index %s: %w", indexPath, verifyErr)
		}
		verified, ok := vscodeIndexFind(verifyEntries, manifest.ID)
		if !ok || !vscodeIndexEntryOwned(verified, target) {
			return fmt.Errorf("VS Code extension index %s does not register %s after write", indexPath, manifest.ID)
		}
		return nil
	})
}

// unregisterVSCodeExtension is registerVSCodeExtension's uninstall half. It
// removes exactly the entry whose relativeLocation names pfm's own link
// folder — the caller (unwireVSCode) has already verified the symlink at
// this index's target still resolves to pfm's source before calling this,
// so the removal predicate only needs to be structurally precise: the
// extension folder name (vscodeExtensionLinkName, fixed) is what "ours"
// means here, never the extension id, which a foreign marketplace install of
// the same extension could also carry under a different relativeLocation.
func (installer *engine) unregisterVSCodeExtension(indexPath string) error {
	entries, err := readVSCodeExtensionIndex(indexPath)
	if err != nil {
		installer.skip("VS Code extension index " + indexPath + " unreadable: " + err.Error() + " — left in place")
		return nil
	}
	kept := make([]map[string]any, 0, len(entries))
	removed := false
	for _, entry := range entries {
		if relativeLocation, _ := entry["relativeLocation"].(string); relativeLocation == vscodeExtensionLinkName {
			removed = true
			continue
		}
		kept = append(kept, entry)
	}
	if !removed {
		return nil
	}
	encoded, err := json.MarshalIndent(kept, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	return installer.change("remove professor.professor from "+indexPath, func() error {
		return atomicfile.Write(indexPath, encoded, 0o644)
	})
}

// VSCodeProductStatus is doctor's one row per recorded extension link
// target: whether the link itself resolves to pfm's source, and — the
// question the link alone cannot answer — whether the product's OWN index
// would actually load it.
type VSCodeProductStatus struct {
	Root       string
	LinkTarget string
	// LinkState is one of "ok", "broken", "missing".
	LinkState string
	IndexPath string
	// IndexState is one of "registered", "missing", "unreadable" — always
	// meaningful even when LinkState != "ok", so a repaired link's index
	// state is never reported as an unrelated "absent".
	IndexState string
	IndexError string
	Version    string
}

// VSCodeSettingsStatus is doctor's one row per owned settings file.
type VSCodeSettingsStatus struct {
	Path string
	// Profile is one of "owned", "relinquished", "missing", "unreadable".
	Profile string
	Default string
	// Error carries a read or decode failure's text when Profile ==
	// "unreadable" — an error never renders as bare absence (issue #24 F6):
	// one settings file's unreadable state names its own cause instead of
	// aborting InspectVSCode and silently dropping every other row.
	Error string
}

// VSCodeReport is InspectVSCode's answer — the product-index reader doctor
// and install share, so `pfm doctor` can never assert a state the installer
// itself did not derive the same way.
type VSCodeReport struct {
	Managed  bool
	Products []VSCodeProductStatus
	Settings []VSCodeSettingsStatus
}

// InspectVSCode reads the VS Code ownership ledger and reports, per
// recorded extension link target, the link's own state AND the product's
// own index state — the two questions 9a/9b showed a link-only report can
// never distinguish, plus one row per owned settings file. A host that never
// opted in (no ledger) reports Managed=false and nothing else: a host that
// never ran `pfm install --vscode` is not a broken one.
func InspectVSCode(home string) (VSCodeReport, error) {
	managedRoot := managedRootForHome(home)
	ownershipPath := filepath.Join(managedRoot, vscodeOwnershipName)
	ownership, extensions, _, _, err := readVSCodeOwnership(ownershipPath)
	if err != nil {
		return VSCodeReport{}, fmt.Errorf("read VS Code ownership %s: %w", ownershipPath, err)
	}
	if len(ownership) == 0 && len(extensions) == 0 {
		return VSCodeReport{}, nil
	}
	manifest, manifestErr := vscodeExtensionManifest()

	report := VSCodeReport{Managed: true}
	source := filepath.Join(managedRoot, filepath.FromSlash(vscodeExtensionSource))

	sortedTargets := append([]string(nil), extensions...)
	sort.Strings(sortedTargets)
	for _, target := range sortedTargets {
		extensionsDir := filepath.Dir(target)
		root := filepath.Dir(extensionsDir)
		status := VSCodeProductStatus{Root: root, IndexPath: filepath.Join(extensionsDir, vscodeExtensionIndexName)}

		if _, statErr := os.Lstat(target); errors.Is(statErr, fs.ErrNotExist) {
			status.LinkState = string(HostOverlayMissing)
		} else if current, linked := resolvedLink(target); linked && current == filepath.Clean(source) {
			status.LinkState = "ok"
		} else {
			status.LinkState = stateBroken
			status.LinkTarget = current
		}

		if manifestErr != nil {
			status.IndexState = MCPClientUnreadable
			status.IndexError = manifestErr.Error()
		} else if entries, indexErr := readVSCodeExtensionIndex(status.IndexPath); indexErr != nil {
			status.IndexState = MCPClientUnreadable
			status.IndexError = indexErr.Error()
		} else if entry, found := vscodeIndexFind(entries, manifest.ID); found && vscodeIndexEntryOwned(entry, target) {
			status.IndexState = "registered"
			status.Version, _ = entry["version"].(string)
		} else {
			status.IndexState = string(HostOverlayMissing)
		}
		report.Products = append(report.Products, status)
	}

	sortedSettings := make([]string, 0, len(ownership))
	for path := range ownership {
		sortedSettings = append(sortedSettings, path)
	}
	sort.Strings(sortedSettings)
	for _, path := range sortedSettings {
		record := ownership[path]
		status := VSCodeSettingsStatus{Path: path}
		raw, readErr := os.ReadFile(path)
		switch {
		case errors.Is(readErr, fs.ErrNotExist):
			if record.ProfileOwned {
				status.Profile = string(HostOverlayMissing)
			} else {
				status.Profile = "relinquished"
			}
		case readErr != nil:
			// A non-ENOENT read error (EACCES, EIO, …) on ONE settings file
			// is this row's own state, never a reason to abort the whole
			// report and drop every other row's already-classified state
			// (issue #24 F6).
			status.Profile = MCPClientUnreadable
			status.Error = readErr.Error()
		default:
			document, decodeErr := decodeJSONCObject(raw)
			if decodeErr != nil {
				status.Profile = MCPClientUnreadable
				status.Error = decodeErr.Error()
				break
			}
			profileKey, defaultKey := vscodeSettingKeys(record.Platform)
			profiles, _ := document[profileKey].(map[string]any)
			_, hasProfile := profiles[vscodeProfileName]
			switch {
			case record.ProfileOwned && hasProfile:
				status.Profile = "owned"
			case record.ProfileOwned && !hasProfile:
				status.Profile = string(HostOverlayMissing)
			default:
				status.Profile = "relinquished"
			}
			if defaultValue, ok := document[defaultKey].(string); ok {
				status.Default = defaultValue
			}
		}
		if status.Default == "" {
			status.Default = "(none)"
		}
		report.Settings = append(report.Settings, status)
	}
	return report, nil
}
