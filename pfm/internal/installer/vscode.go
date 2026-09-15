package installer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"

	"hostops/pfm/internal/atomicfile"
)

const (
	vscodeOwnershipName    = "vscode-ownership.json"
	vscodeOwnershipVersion = 1
	// vscodeProfileName is the settings profile pfm writes AND the default
	// terminal it selects. The default must stay a SETTINGS profile: a window
	// reload rebuilds every restored terminal through createTerminal, and VS
	// Code's getContributedDefaultProfile hands a restored terminal (no
	// executable, no extHostTerminalId) to an EXTENSION-contributed default —
	// the extension makes a brand-new terminal, the live one is never
	// reattached, and the pty host shuts it down after its grace time.
	vscodeProfileName = "PFM"
	// vscodeExtensionProfileTitle is the terminal profile the Professor
	// extension contributes (its title in assets/vscode/professor/package.json):
	// offered in the + dropdown, never selected as the default. An owned
	// default holding it — written by the release that briefly selected it —
	// is pfm's own earlier value and moves back to vscodeProfileName. The
	// professor.newChatTerminal command now delegates to this same
	// contributed-profile route (workbench.action.terminal.newWithProfile)
	// instead of building its own createTerminal options, and the extension
	// carries a default keybinding for it (extension.js, package.json).
	vscodeExtensionProfileTitle = "Professor"
	// vscodeExtensionLinkName is the folder name pfm links into each VS Code
	// product's extensions directory, and the extension id VS Code records
	// for it.
	vscodeExtensionLinkName = "professor"
	// vscodeExtensionSource is the managed-asset subpath the generic asset
	// walk (assets.go assetFiles) stages the Professor extension under,
	// relative to managedRoot.
	vscodeExtensionSource = "vscode/professor"
	// vscodeExtensionIndexName is the per-product index VS Code itself
	// scans user extensions from (AbstractExtensionsScannerService.
	// scanUserExtensions) — a hand-linked directory absent from it is never
	// loaded, link notwithstanding. See registerVSCodeExtension.
	vscodeExtensionIndexName = "extensions.json"
)

var errMalformedVSCodeSettings = errors.New("malformed VS Code settings")

type vscodeOwnershipDocument struct {
	Version int                     `json:"version"`
	Files   []vscodeOwnershipRecord `json:"files"`
	// Extensions records the extension link paths pfm made — recorded
	// independently of Files because a link survives on a product that never
	// had a settings.json touched (a plain ~/.vscode-server with no Machine
	// settings yet, for instance).
	Extensions []string `json:"extensions,omitempty"`
	// IndexRegistrations records the extensions.json paths pfm appended its
	// professor.professor entry to — one per kept Extensions target, derived
	// the same way (<extensions dir>/extensions.json). Uninstall reads it to
	// know which product indexes to clean; it is never itself the ownership
	// check (that is relativeLocation=="professor" plus the symlink still
	// resolving to pfm's source — see unwireVSCode).
	IndexRegistrations []string `json:"indexRegistrations,omitempty"`
}

type vscodeOwnershipRecord struct {
	Path                  string          `json:"path"`
	Platform              string          `json:"platform"`
	FileAdded             bool            `json:"fileAdded,omitempty"`
	ProfileOwned          bool            `json:"profileOwned,omitempty"`
	ProfilesPropertyAdded bool            `json:"profilesPropertyAdded,omitempty"`
	DefaultOwned          bool            `json:"defaultOwned,omitempty"`
	HadDefault            bool            `json:"hadDefault,omitempty"`
	PreviousDefault       json.RawMessage `json:"previousDefault,omitempty"`
	// ScalarOwned/HadScalar/PreviousScalar extend DefaultOwned/HadDefault/
	// PreviousDefault's exact relinquish-then-claim shape to every key in
	// vscodeScalarKeys: tmux (`tmux -L cc-*`) is a chat's survival layer, the
	// VS Code tab only a view onto it — enablePersistentSessions reconnects
	// the view across a window reload; persistentSessionReviveProcess stays
	// "never" because reviving a dead tab after a server death spawns one
	// live picker per tab, a CPU storm (2026-09-03, see internal/ui's
	// idle-picker backoff); showExitAlert off drops the per-chat exit toast;
	// remote.autoForwardPorts off stops VS Code auto-forwarding a fleet
	// chat's stray listening port.
	ScalarOwned    map[string]bool            `json:"scalarOwned,omitempty"`
	HadScalar      map[string]bool            `json:"hadScalar,omitempty"`
	PreviousScalar map[string]json.RawMessage `json:"previousScalar,omitempty"`
}

func (installer *engine) wireVSCode() error {
	ownershipPath := filepath.Join(installer.managedRoot, vscodeOwnershipName)
	ownership, extensions, indexRegistrations, ownershipRaw, err := readVSCodeOwnership(ownershipPath)
	if err != nil {
		return fmt.Errorf("read VS Code ownership %s: %w", ownershipPath, err)
	}
	if installer.options.Mode == ModeUninstall {
		return installer.unwireVSCode(ownershipPath, ownershipRaw, ownership, extensions, indexRegistrations)
	}
	if !installer.options.VSCode && len(ownership) == 0 && len(extensions) == 0 {
		return nil
	}

	livePlatform, err := installer.vscodePlatform()
	if err != nil {
		return err
	}
	paths := make(map[string]bool, len(ownership))
	for path := range ownership {
		paths[path] = true
	}
	if installer.options.VSCode {
		for _, path := range installer.vscodeSettingsPaths() {
			paths[path] = true
		}
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)

	for _, path := range ordered {
		record, alreadyOwned := ownership[path]
		if !alreadyOwned {
			record = vscodeOwnershipRecord{Path: path, Platform: livePlatform}
		}
		updated, next, changed, err := installer.mergeVSCodeSettings(path, record, alreadyOwned)
		if err != nil {
			if errors.Is(err, errMalformedVSCodeSettings) {
				installer.skip("VS Code settings skipped " + path + ": " + err.Error())
				continue
			}
			return err
		}
		if next.ProfileOwned || next.DefaultOwned || len(next.ScalarOwned) != 0 {
			ownership[path] = next
		} else {
			delete(ownership, path)
		}
		if !changed {
			installer.ok("VS Code PFM terminal profile " + path)
			continue
		}
		if err := installer.change("merge VS Code PFM terminal profile "+path, func() error {
			return installer.writeVSCodeSettings(path, updated)
		}); err != nil {
			return err
		}
	}
	extensions, err = installer.linkVSCodeExtension(extensions)
	if err != nil {
		return err
	}
	indexRegistrations = make([]string, 0, len(extensions))
	for _, target := range extensions {
		indexRegistrations = append(indexRegistrations, filepath.Join(filepath.Dir(target), vscodeExtensionIndexName))
	}
	return installer.writeVSCodeOwnership(ownershipPath, ownershipRaw, ownership, extensions, indexRegistrations)
}

// vscodeExtensionLinks enumerates the extension-link targets pfm can wire on
// this host: <product root>/extensions/professor for every VS Code product
// root that actually EXISTS under the home directory, plus the portable
// install's data directory — VSCODE_PORTABLE names that directory itself
// (VS Code's bootstrap-node getPortableDataPath), so its extensions/ sits
// directly beneath it. A product a user never installed gets no link — pfm
// never creates the product's own directory tree, only extends one that is
// already there.
func (installer *engine) vscodeExtensionLinks() []string {
	roots := installer.options.vscodeExtensionRoots
	if roots == nil {
		home := installer.options.Home
		roots = []string{
			filepath.Join(home, ".vscode"),
			filepath.Join(home, ".vscode-insiders"),
			filepath.Join(home, ".vscode-oss"),
			filepath.Join(home, ".vscode-server"),
			filepath.Join(home, ".vscode-server-insiders"),
		}
		if portable := os.Getenv("VSCODE_PORTABLE"); filepath.IsAbs(portable) {
			roots = append(roots, portable)
		}
	}
	var found []string
	for _, root := range roots {
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			found = append(found, filepath.Join(root, "extensions", vscodeExtensionLinkName))
		}
	}
	return cleanUniquePaths(found)
}

// linkVSCodeExtension reconciles the set of extension links pfm owns:
// previously recorded targets plus every currently discoverable product
// root. It runs only on a VS Code-managed install (the flag, or a non-empty
// ledger), and discovery is unconditional there: the extension is part of the
// managed VS Code surface, so a ledger written before it shipped gets it on
// the next ordinary install. A recorded target whose product was uninstalled
// (its root directory is gone) is dropped by name rather than recreating a
// directory tree nothing else uses; every other target gets its extensions/
// directory created if needed and the link itself made idempotent through
// ensureLink. It returns the kept targets, sorted, for the ownership ledger.
func (installer *engine) linkVSCodeExtension(recorded []string) ([]string, error) {
	source := filepath.Join(installer.managedRoot, filepath.FromSlash(vscodeExtensionSource))
	targets := make(map[string]bool, len(recorded))
	for _, target := range recorded {
		targets[target] = true
	}
	for _, target := range installer.vscodeExtensionLinks() {
		targets[target] = true
	}
	ordered := make([]string, 0, len(targets))
	for target := range targets {
		ordered = append(ordered, target)
	}
	sort.Strings(ordered)

	var kept []string
	for _, target := range ordered {
		extensionsDir := filepath.Dir(target)
		root := filepath.Dir(extensionsDir)
		if info, err := os.Stat(root); err != nil || !info.IsDir() {
			installer.skip(target + " VS Code product " + root + " no longer exists; dropped extension link")
			continue
		}
		if info, err := os.Stat(extensionsDir); err != nil || !info.IsDir() {
			if err := installer.change("create "+extensionsDir, func() error {
				return os.MkdirAll(extensionsDir, 0o755)
			}); err != nil {
				return nil, fmt.Errorf("link VS Code extension %s: %w", target, err)
			}
		}
		if _, err := installer.ensureLink(source, target); err != nil {
			return nil, fmt.Errorf("link VS Code extension %s: %w", target, err)
		}
		if _, err := installer.registerVSCodeExtension(extensionsDir); err != nil {
			return nil, fmt.Errorf("register VS Code extension %s: %w", target, err)
		}
		kept = append(kept, target)
	}
	sort.Strings(kept)
	return kept, nil
}

func (installer *engine) mergeVSCodeSettings(
	path string,
	record vscodeOwnershipRecord,
	alreadyOwned bool,
) ([]byte, vscodeOwnershipRecord, bool, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		raw = []byte("{}\n")
		if !alreadyOwned {
			record.FileAdded = true
		}
	} else if err != nil {
		return nil, record, false, fmt.Errorf("read VS Code settings %s: %w", path, err)
	}
	document, err := decodeJSONCObject(raw)
	if err != nil {
		return nil, record, false, fmt.Errorf(
			"%w: decode VS Code settings %s: %v",
			errMalformedVSCodeSettings,
			path,
			err,
		)
	}
	profileKey, defaultKey := vscodeSettingKeys(record.Platform)
	canonical := vscodeProfile()

	profilesValue, hasProfiles := document[profileKey]
	var profiles map[string]any
	if hasProfiles {
		var ok bool
		profiles, ok = profilesValue.(map[string]any)
		if !ok {
			return nil, record, false, fmt.Errorf(
				"%w: VS Code settings %s: %s must be an object",
				errMalformedVSCodeSettings,
				path,
				profileKey,
			)
		}
	} else {
		profiles = map[string]any{}
	}
	existingProfile, hasProfile := profiles[vscodeProfileName]
	// A profile byte-identical to a shape pfm itself has ever written is never an
	// operator customization worth refusing over — UNCONDITIONALLY, not just when
	// the ownership ledger already tracks it: a real devbox backup (see
	// realMalformedVSCodeSettings) can carry a hand-inserted PFM block that
	// happens to match an old canonical shape with no ledger behind it at all,
	// and that must read as "already correct," the same verdict an exact CURRENT
	// canonical match gets below, never as a conflicting operator profile.
	upgradingProfile := isLegacyVSCodeProfile(existingProfile)
	profileRelinquished := false
	if alreadyOwned && record.ProfileOwned && hasProfile && !reflect.DeepEqual(existingProfile, canonical) &&
		!upgradingProfile {
		// A user edit after installation wins. Relinquish this field instead of
		// rewriting it during an unrelated update.
		record.ProfileOwned = false
		record.ProfilesPropertyAdded = false
		profileRelinquished = true
	}
	if installer.options.VSCode || record.ProfileOwned {
		if hasProfile && !reflect.DeepEqual(existingProfile, canonical) && !upgradingProfile {
			if !profileRelinquished {
				return nil, record, false, fmt.Errorf(
					"VS Code settings %s: profile %q already exists and is not PFM-owned",
					path,
					vscodeProfileName,
				)
			}
		}
		if !hasProfile {
			record.ProfileOwned = true
			record.ProfilesPropertyAdded = !hasProfiles
		}
	}

	existingDefault, hasDefault := document[defaultKey]
	if alreadyOwned && record.DefaultOwned &&
		(!hasDefault || (existingDefault != vscodeProfileName && existingDefault != vscodeExtensionProfileTitle)) {
		// An owned default holding the extension's title is pfm's own earlier
		// value, moved back — not an operator override. Only a THIRD value
		// (something the operator picked after installation) relinquishes.
		record.DefaultOwned = false
		record.HadDefault = false
		record.PreviousDefault = nil
	}
	if installer.options.VSCode && !record.DefaultOwned && (!hasDefault || existingDefault != vscodeProfileName) {
		record.DefaultOwned = true
		record.HadDefault = hasDefault
		if hasDefault {
			record.PreviousDefault, err = json.Marshal(existingDefault)
			if err != nil {
				return nil, record, false, fmt.Errorf("preserve VS Code default in %s: %w", path, err)
			}
		}
	}

	for _, key := range vscodeScalarKeys {
		want := vscodeScalarValue(key)
		existing, hasExisting := document[key]
		if alreadyOwned && record.ScalarOwned[key] && (!hasExisting || existing != want) {
			// An operator's own edit after installation wins, same as DefaultOwned.
			delete(record.ScalarOwned, key)
			delete(record.HadScalar, key)
			delete(record.PreviousScalar, key)
		}
		if installer.options.VSCode && !record.ScalarOwned[key] && (!hasExisting || existing != want) {
			if record.ScalarOwned == nil {
				record.ScalarOwned = map[string]bool{}
			}
			record.ScalarOwned[key] = true
			if hasExisting {
				previousRaw, marshalErr := json.Marshal(existing)
				if marshalErr != nil {
					return nil, record, false, fmt.Errorf("preserve VS Code %s in %s: %w", key, path, marshalErr)
				}
				if record.HadScalar == nil {
					record.HadScalar = map[string]bool{}
				}
				record.HadScalar[key] = true
				if record.PreviousScalar == nil {
					record.PreviousScalar = map[string]json.RawMessage{}
				}
				record.PreviousScalar[key] = previousRaw
			}
		}
	}

	updated := append([]byte(nil), raw...)
	changed := false
	if record.ProfileOwned && !reflect.DeepEqual(existingProfile, canonical) {
		profileRaw, marshalErr := json.MarshalIndent(canonical, "", "  ")
		if marshalErr != nil {
			return nil, record, false, marshalErr
		}
		if hasProfiles {
			root, parseErr := parseJSONCObject(updated, 0)
			if parseErr != nil {
				return nil, record, false, parseErr
			}
			property := root.byName[profileKey]
			updated, parseErr = setJSONCProperty(updated, property.valueStart, vscodeProfileName, profileRaw)
			if parseErr != nil {
				return nil, record, false, parseErr
			}
		} else {
			profilesRaw, marshalErr := json.MarshalIndent(map[string]any{vscodeProfileName: canonical}, "", "  ")
			if marshalErr != nil {
				return nil, record, false, marshalErr
			}
			updated, err = setJSONCProperty(updated, 0, profileKey, profilesRaw)
			if err != nil {
				return nil, record, false, err
			}
		}
		changed = true
	}
	if record.DefaultOwned && (!hasDefault || existingDefault != vscodeProfileName) {
		updated, err = setJSONCProperty(updated, 0, defaultKey, []byte(`"`+vscodeProfileName+`"`))
		if err != nil {
			return nil, record, false, err
		}
		changed = true
	}
	for _, key := range vscodeScalarKeys {
		if !record.ScalarOwned[key] {
			continue
		}
		want := vscodeScalarValue(key)
		if existing, hasExisting := document[key]; hasExisting && existing == want {
			continue
		}
		valueRaw, marshalErr := json.Marshal(want)
		if marshalErr != nil {
			return nil, record, false, marshalErr
		}
		updated, err = setJSONCProperty(updated, 0, key, valueRaw)
		if err != nil {
			return nil, record, false, err
		}
		changed = true
	}
	return updated, record, changed, nil
}

func (installer *engine) unwireVSCode(
	path string,
	existing []byte,
	ownership map[string]vscodeOwnershipRecord,
	extensions, indexRegistrations []string,
) error {
	ordered := make([]string, 0, len(ownership))
	for settings := range ownership {
		ordered = append(ordered, settings)
	}
	sort.Strings(ordered)
	for _, settings := range ordered {
		record := ownership[settings]
		raw, err := os.ReadFile(settings)
		if errors.Is(err, fs.ErrNotExist) {
			delete(ownership, settings)
			continue
		}
		if err != nil {
			return fmt.Errorf("read VS Code settings %s: %w", settings, err)
		}
		document, err := decodeJSONCObject(raw)
		if err != nil {
			installer.skip(
				"VS Code settings skipped " + settings + ": " + fmt.Errorf("%w: decode VS Code settings: %v", errMalformedVSCodeSettings, err).
					Error(),
			)
			continue
		}
		profileKey, defaultKey := vscodeSettingKeys(record.Platform)
		updated := append([]byte(nil), raw...)
		changed := false
		if record.DefaultOwned &&
			(document[defaultKey] == vscodeProfileName || document[defaultKey] == vscodeExtensionProfileTitle) {
			if record.HadDefault {
				updated, err = setJSONCProperty(updated, 0, defaultKey, record.PreviousDefault)
			} else {
				updated, err = removeJSONCProperty(updated, 0, defaultKey)
			}
			if err != nil {
				return err
			}
			changed = true
			record.DefaultOwned = false
			record.HadDefault = false
			record.PreviousDefault = nil
		}
		for _, key := range vscodeScalarKeys {
			if !record.ScalarOwned[key] || document[key] != vscodeScalarValue(key) {
				continue
			}
			if record.HadScalar[key] {
				updated, err = setJSONCProperty(updated, 0, key, record.PreviousScalar[key])
			} else {
				updated, err = removeJSONCProperty(updated, 0, key)
			}
			if err != nil {
				return err
			}
			changed = true
			delete(record.ScalarOwned, key)
			delete(record.HadScalar, key)
			delete(record.PreviousScalar, key)
		}
		profileRetained := false
		if record.ProfileOwned {
			// Re-decode after the root edit because byte offsets have changed.
			current, decodeErr := decodeJSONCObject(updated)
			if decodeErr != nil {
				return decodeErr
			}
			profiles, _ := current[profileKey].(map[string]any)
			profile, hasProfile := profiles[vscodeProfileName]
			if reflect.DeepEqual(profile, vscodeProfile()) || isLegacyVSCodeProfile(profile) {
				if record.ProfilesPropertyAdded && len(profiles) == 1 {
					updated, err = removeJSONCProperty(updated, 0, profileKey)
				} else {
					root, parseErr := parseJSONCObject(updated, 0)
					if parseErr != nil {
						return parseErr
					}
					updated, err = removeJSONCProperty(updated, root.byName[profileKey].valueStart, vscodeProfileName)
				}
				if err != nil {
					return err
				}
				changed = true
				record.ProfileOwned = false
				record.ProfilesPropertyAdded = false
			} else if hasProfile {
				profileRetained = true
			}
		}
		if changed {
			removeEmptyFile := false
			if record.FileAdded {
				current, decodeErr := decodeJSONCObject(updated)
				if decodeErr != nil {
					return decodeErr
				}
				removeEmptyFile = len(current) == 0 && !bytes.Contains(updated, []byte("//")) &&
					!bytes.Contains(updated, []byte("/*"))
			}
			if err := installer.change("remove VS Code PFM terminal profile "+settings, func() error {
				if removeEmptyFile {
					return os.Remove(settings)
				}
				return installer.writeVSCodeSettings(settings, updated)
			}); err != nil {
				return err
			}
		} else {
			installer.ok("VS Code settings preserved " + settings)
		}
		if profileRetained {
			ownership[settings] = record
			installer.skip(
				"VS Code PFM terminal profile was edited; left it in place and retained recovery ownership at " + settings,
			)
		} else {
			delete(ownership, settings)
		}
	}

	source := filepath.Join(installer.managedRoot, filepath.FromSlash(vscodeExtensionSource))
	registeredIndexes := make(map[string]bool, len(indexRegistrations))
	for _, indexPath := range indexRegistrations {
		registeredIndexes[filepath.Clean(indexPath)] = true
	}
	orderedExtensions := append([]string(nil), extensions...)
	sort.Strings(orderedExtensions)
	for _, target := range orderedExtensions {
		current, linked := resolvedLink(target)
		if !linked || current != filepath.Clean(source) {
			installer.skip(target + " no longer points at pfm's Professor extension; left in place")
			continue
		}
		indexPath := filepath.Join(filepath.Dir(target), vscodeExtensionIndexName)
		if registeredIndexes[filepath.Clean(indexPath)] {
			if err := installer.unregisterVSCodeExtension(indexPath); err != nil {
				return err
			}
		}
		if err := installer.unlinkOne(target); err != nil {
			return err
		}
	}

	return installer.writeVSCodeOwnership(path, existing, ownership, nil, nil)
}

// writeVSCodeSettings backs up and atomically rewrites a VS Code settings file
// THROUGH any symlink, as writeMCPFile does for a linked MCP registry. Dotfile
// managers link settings.json into a repository; renaming over the link would
// sever it, leaving VS Code on a detached copy the managed file never sees. A
// file that does not exist yet is created at path with owner-only access.
func (installer *engine) writeVSCodeSettings(path string, content []byte) error {
	physical, mode := path, fs.FileMode(0o600)
	info, err := os.Stat(path)
	switch {
	case err == nil:
		mode = info.Mode().Perm()
		if physical, err = filepath.EvalSymlinks(path); err != nil {
			return fmt.Errorf("resolve VS Code settings %s: %w", path, err)
		}
		if err := copyBackup(physical, availableBackup(physical, installer.stamp)); err != nil {
			return err
		}
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	return atomicfile.Write(physical, content, mode)
}

func (installer *engine) writeVSCodeOwnership(
	path string,
	existing []byte,
	ownership map[string]vscodeOwnershipRecord,
	extensions, indexRegistrations []string,
) error {
	if len(ownership) == 0 && len(extensions) == 0 && len(indexRegistrations) == 0 {
		if len(existing) == 0 {
			return nil
		}
		return installer.change("remove "+path, func() error { return os.Remove(path) })
	}
	document := vscodeOwnershipDocument{Version: vscodeOwnershipVersion}
	for _, record := range ownership {
		document.Files = append(document.Files, record)
	}
	sort.Slice(document.Files, func(i, j int) bool { return document.Files[i].Path < document.Files[j].Path })
	if len(extensions) != 0 {
		document.Extensions = append([]string(nil), extensions...)
		sort.Strings(document.Extensions)
	}
	if len(indexRegistrations) != 0 {
		document.IndexRegistrations = append([]string(nil), indexRegistrations...)
		sort.Strings(document.IndexRegistrations)
	}
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if bytes.Equal(existing, encoded) && sameFile(path, encoded, 0o600) {
		installer.ok(path)
		return nil
	}
	return installer.change("write "+path, func() error { return atomicfile.Write(path, encoded, 0o600) })
}

func readVSCodeOwnership(path string) (map[string]vscodeOwnershipRecord, []string, []string, []byte, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]vscodeOwnershipRecord{}, nil, nil, nil, nil
	}
	if err != nil {
		return nil, nil, nil, nil, err
	}
	var document vscodeOwnershipDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, nil, nil, nil, err
	}
	if document.Version != vscodeOwnershipVersion {
		return nil, nil, nil, nil, fmt.Errorf("unsupported version %d", document.Version)
	}
	records := make(map[string]vscodeOwnershipRecord, len(document.Files))
	for _, record := range document.Files {
		if !filepath.IsAbs(record.Path) || (record.Platform != "linux" && record.Platform != "osx") {
			return nil, nil, nil, nil, fmt.Errorf("invalid record path/platform %q/%q", record.Path, record.Platform)
		}
		if _, duplicate := records[record.Path]; duplicate {
			return nil, nil, nil, nil, fmt.Errorf("duplicate record %s", record.Path)
		}
		records[record.Path] = record
	}
	extensions := make([]string, 0, len(document.Extensions))
	seenExtensions := make(map[string]bool, len(document.Extensions))
	for _, extension := range document.Extensions {
		if !filepath.IsAbs(extension) {
			return nil, nil, nil, nil, fmt.Errorf("invalid extension link path %q", extension)
		}
		if seenExtensions[extension] {
			return nil, nil, nil, nil, fmt.Errorf("duplicate extension link %s", extension)
		}
		seenExtensions[extension] = true
		extensions = append(extensions, extension)
	}
	indexRegistrations := make([]string, 0, len(document.IndexRegistrations))
	seenIndexes := make(map[string]bool, len(document.IndexRegistrations))
	for _, indexPath := range document.IndexRegistrations {
		if !filepath.IsAbs(indexPath) {
			return nil, nil, nil, nil, fmt.Errorf("invalid index registration path %q", indexPath)
		}
		if seenIndexes[indexPath] {
			return nil, nil, nil, nil, fmt.Errorf("duplicate index registration %s", indexPath)
		}
		seenIndexes[indexPath] = true
		indexRegistrations = append(indexRegistrations, indexPath)
	}
	return records, extensions, indexRegistrations, raw, nil
}

func (installer *engine) vscodePlatform() (string, error) {
	platform := installer.options.vscodePlatform
	if platform == "" {
		platform = runtime.GOOS
	}
	switch platform {
	case "darwin", "osx":
		return "osx", nil
	case "linux":
		return "linux", nil
	default:
		return "", fmt.Errorf("VS Code PFM terminal profile is unsupported on %s", platform)
	}
}

func (installer *engine) vscodeSettingsPaths() []string {
	if installer.options.vscodeSettingsPaths != nil {
		return cleanUniquePaths(installer.options.vscodeSettingsPaths)
	}
	home := installer.options.Home
	platform, _ := installer.vscodePlatform()
	var canonical string
	var candidates []string
	if platform == "osx" {
		canonical = filepath.Join(home, "Library", "Application Support", "Code", "User", "settings.json")
		candidates = []string{
			canonical,
			filepath.Join(home, "Library", "Application Support", "Code - Insiders", "User", "settings.json"),
			filepath.Join(home, "Library", "Application Support", "VSCodium", "User", "settings.json"),
		}
	} else {
		configRoot := os.Getenv("XDG_CONFIG_HOME")
		if !filepath.IsAbs(configRoot) {
			configRoot = filepath.Join(home, ".config")
		}
		canonical = filepath.Join(configRoot, "Code", "User", "settings.json")
		candidates = []string{
			canonical,
			filepath.Join(configRoot, "Code - Insiders", "User", "settings.json"),
			filepath.Join(configRoot, "VSCodium", "User", "settings.json"),
			filepath.Join(home, ".vscode-server", "data", "Machine", "settings.json"),
			filepath.Join(home, ".vscode-server-insiders", "data", "Machine", "settings.json"),
		}
	}
	if portable := os.Getenv("VSCODE_PORTABLE"); filepath.IsAbs(portable) {
		// VSCODE_PORTABLE is the portable data directory itself, not the
		// install folder holding it (see vscodeExtensionLinks).
		candidates = append(candidates, filepath.Join(portable, "user-data", "User", "settings.json"))
	}
	var found []string
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			found = append(found, candidate)
			continue
		}
		if info, err := os.Stat(filepath.Dir(candidate)); err == nil && info.IsDir() {
			found = append(found, candidate)
		}
	}
	if len(found) == 0 {
		found = []string{canonical}
	}
	return cleanUniquePaths(found)
}

func cleanUniquePaths(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	var result []string
	for _, path := range paths {
		if path == "" {
			continue
		}
		path = filepath.Clean(path)
		if !seen[path] {
			seen[path] = true
			result = append(result, path)
		}
	}
	sort.Strings(result)
	return result
}

func vscodeSettingKeys(platform string) (string, string) {
	return "terminal.integrated.profiles." + platform, "terminal.integrated.defaultProfile." + platform
}

// vscodeScalarKeys are the fixed-value settings pfm install owns besides the
// default-profile key (handled separately — it names this profile, not a
// constant). Unlike terminal.integrated.profiles.<platform>/defaultProfile,
// these are not platform-suffixed: VS Code reads them the same on every OS.
var vscodeScalarKeys = []string{
	"terminal.integrated.enablePersistentSessions",
	"terminal.integrated.persistentSessionReviveProcess",
	"terminal.integrated.showExitAlert",
	"remote.autoForwardPorts",
}

// vscodeScalarValue is the value pfm install owns key to. See
// vscodeOwnershipRecord.ScalarOwned for why each one is what it is.
func vscodeScalarValue(key string) any {
	switch key {
	case "terminal.integrated.enablePersistentSessions":
		return true
	case "terminal.integrated.persistentSessionReviveProcess":
		return "never"
	case "terminal.integrated.showExitAlert":
		return false
	case "remote.autoForwardPorts":
		return false
	default:
		return nil
	}
}
