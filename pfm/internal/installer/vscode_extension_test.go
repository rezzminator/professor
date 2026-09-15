package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// newVSCodeExtensionEngine builds the minimal *engine a linkVSCodeExtension /
// wireVSCode test needs, mirroring the low-level construction
// TestVSCodeNewPathUsesLivePlatformNotAnOlderRecordsPlatform already uses in
// vscode_test.go: vscodeSettingsPaths is pinned to an EMPTY (non-nil) slice
// so the terminal-profile merge never runs, keeping every test here focused
// on the extension link and its ledger.
func newVSCodeExtensionEngine(home string, roots []string, vscodeFlag bool) *engine {
	return &engine{
		options: Options{
			Mode: ModeApply, Home: home, Runner: &fakeRunner{}, Stdout: &bytes.Buffer{},
			VSCode: vscodeFlag, vscodePlatform: "linux", vscodeSettingsPaths: []string{},
			vscodeExtensionRoots: roots,
		},
		apply:       true,
		managedRoot: filepath.Join(home, ".local", "share", "pfm", "install"),
		stamp:       "fixture",
	}
}

func readVSCodeLedgerFixture(t *testing.T, managedRoot string) vscodeOwnershipDocument {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(managedRoot, vscodeOwnershipName))
	if err != nil {
		t.Fatal(err)
	}
	var document vscodeOwnershipDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	return document
}

// TestVSCodeExtensionLinksIntoEveryPresentProductRootNeverAnAbsentOne pins
// vscodeExtensionLinks' root os.Stat gate: a product root that exists gets
// linked, a seam entry naming a root nobody installed gets neither a link
// nor a created directory tree.
func TestVSCodeExtensionLinksIntoEveryPresentProductRootNeverAnAbsentOne(t *testing.T) {
	t.Setenv("VSCODE_PORTABLE", "")
	home := t.TempDir()
	rootA := filepath.Join(home, "product-a")
	rootB := filepath.Join(home, "product-b")
	rootMissing := filepath.Join(home, "product-missing")
	for _, root := range []string{rootA, rootB} {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	installer := newVSCodeExtensionEngine(home, []string{rootA, rootB, rootMissing}, true)
	if err := installer.wireVSCode(); err != nil {
		t.Fatal(err)
	}

	source := filepath.Join(installer.managedRoot, filepath.FromSlash(vscodeExtensionSource))
	for _, root := range []string{rootA, rootB} {
		target := filepath.Join(root, "extensions", vscodeExtensionLinkName)
		resolved, linked := resolvedLink(target)
		if !linked || resolved != filepath.Clean(source) {
			t.Fatalf("expected %s linked to %s, got resolved=%q linked=%v", target, source, resolved, linked)
		}
	}
	if _, err := os.Stat(rootMissing); !os.IsNotExist(err) {
		t.Fatalf("a VS Code product root nobody installed was created: %v", err)
	}

	ledger := readVSCodeLedgerFixture(t, installer.managedRoot)
	want := []string{
		filepath.Join(rootA, "extensions", vscodeExtensionLinkName),
		filepath.Join(rootB, "extensions", vscodeExtensionLinkName),
	}
	sort.Strings(want)
	if !reflect.DeepEqual(ledger.Extensions, want) {
		t.Fatalf("ledger extensions = %v, want %v", ledger.Extensions, want)
	}
}

// TestVSCodeExtensionReinstallIsIdempotent proves a second apply over the
// same roots changes nothing: no new "change" is reported and the ledger
// bytes are byte-for-byte the same, the same law ensureLink already gives
// every other managed link.
func TestVSCodeExtensionReinstallIsIdempotent(t *testing.T) {
	t.Setenv("VSCODE_PORTABLE", "")
	home := t.TempDir()
	root := filepath.Join(home, "product-a")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	roots := []string{root}

	first := newVSCodeExtensionEngine(home, roots, true)
	if err := first.wireVSCode(); err != nil {
		t.Fatal(err)
	}
	ledgerPath := filepath.Join(first.managedRoot, vscodeOwnershipName)
	before, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "extensions", vscodeExtensionLinkName)
	beforeLink, _ := resolvedLink(target)

	second := newVSCodeExtensionEngine(home, roots, true)
	if err := second.wireVSCode(); err != nil {
		t.Fatal(err)
	}
	if second.report.Changed != 0 {
		t.Fatalf(
			"second apply reported %d changes, want 0:\n%s",
			second.report.Changed,
			second.options.Stdout.(*bytes.Buffer).String(),
		)
	}
	after, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("ledger bytes changed on an idempotent reinstall:\nbefore=%s\nafter=%s", before, after)
	}
	afterLink, _ := resolvedLink(target)
	if beforeLink != afterLink {
		t.Fatalf("link target changed on an idempotent reinstall: %q -> %q", beforeLink, afterLink)
	}
}

// TestVSCodeExtensionBacksUpAndRestoresARealNonLinkTarget covers the
// non-symlink branch of ensureLink for an extensions/professor that is
// already a real directory (an operator-installed extension of the same
// name, or a leftover from a manual copy): install backs it up and replaces
// it with the link, uninstall restores the original directory verbatim.
func TestVSCodeExtensionBacksUpAndRestoresARealNonLinkTarget(t *testing.T) {
	t.Setenv("VSCODE_PORTABLE", "")
	home := t.TempDir()
	root := filepath.Join(home, "product-a")
	target := filepath.Join(root, "extensions", vscodeExtensionLinkName)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(target, "marker.txt")
	if err := os.WriteFile(marker, []byte("operator content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	roots := []string{root}

	installer := newVSCodeExtensionEngine(home, roots, true)
	if err := installer.wireVSCode(); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(installer.managedRoot, filepath.FromSlash(vscodeExtensionSource))
	resolved, linked := resolvedLink(target)
	if !linked || resolved != filepath.Clean(source) {
		t.Fatalf("real directory was not replaced by the link: resolved=%q linked=%v", resolved, linked)
	}
	backups, err := filepath.Glob(target + ".pre-professor-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("expected exactly one backup of the real directory, got %v", backups)
	}
	if got, err := os.ReadFile(
		filepath.Join(backups[0], "marker.txt"),
	); err != nil ||
		string(got) != "operator content\n" {
		t.Fatalf("backup lost the marker file: err=%v content=%q", err, got)
	}

	uninstaller := newVSCodeExtensionEngine(home, roots, false)
	uninstaller.options.Mode = ModeUninstall
	if err := uninstaller.wireVSCode(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(target)
	if err != nil {
		t.Fatalf("uninstall removed the restored directory: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("uninstall left a symlink instead of restoring the real directory")
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "operator content\n" {
		t.Fatalf("restored directory lost its marker: err=%v content=%q", err, got)
	}
}

// TestVSCodeExtensionUninstallSkipsAForeignRelinkedTargetButStillDeletesTheLedger
// pins the resolvedLink == source guard in unwireVSCode: a target repointed
// elsewhere after install is not pfm's link anymore, so uninstall must leave
// it exactly alone and name it in the skip output — but the ledger itself is
// still cleared, because unwireVSCode always writes back with nil
// extensions regardless of what it could not restore.
func TestVSCodeExtensionUninstallSkipsAForeignRelinkedTargetButStillDeletesTheLedger(t *testing.T) {
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
	target := filepath.Join(root, "extensions", vscodeExtensionLinkName)

	foreign := t.TempDir()
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, target); err != nil {
		t.Fatal(err)
	}

	uninstaller := newVSCodeExtensionEngine(home, roots, false)
	uninstaller.options.Mode = ModeUninstall
	var output bytes.Buffer
	uninstaller.options.Stdout = &output
	if err := uninstaller.wireVSCode(); err != nil {
		t.Fatal(err)
	}

	resolved, linked := resolvedLink(target)
	if !linked || resolved != filepath.Clean(foreign) {
		t.Fatalf("uninstall touched the foreign-relinked target: resolved=%q linked=%v", resolved, linked)
	}
	if !strings.Contains(output.String(), target) ||
		!strings.Contains(output.String(), "no longer points at pfm's Professor extension") {
		t.Fatalf("uninstall did not name the skipped foreign link:\n%s", output.String())
	}
	if _, err := os.Stat(filepath.Join(installer.managedRoot, vscodeOwnershipName)); !os.IsNotExist(err) {
		t.Fatalf("uninstall retained the ledger despite no remaining ownership: %v", err)
	}
}

// TestVSCodeExtensionLedgerRoundTripsSortedAndValidates pins
// writeVSCodeOwnership/readVSCodeOwnership directly: extensions come back
// sorted regardless of write order, a relative extension path is refused,
// and a duplicate is refused.
func TestVSCodeExtensionLedgerRoundTripsSortedAndValidates(t *testing.T) {
	home := t.TempDir()
	managed := filepath.Join(home, ".local", "share", "pfm", "install")
	path := filepath.Join(managed, vscodeOwnershipName)
	installer := &engine{
		options:     Options{Home: home, Stdout: &bytes.Buffer{}},
		apply:       true,
		managedRoot: managed,
		stamp:       "fixture",
	}

	unsorted := []string{
		filepath.Join(home, "z-product", "extensions", "professor"),
		filepath.Join(home, "a-product", "extensions", "professor"),
	}
	if err := installer.writeVSCodeOwnership(path, nil, map[string]vscodeOwnershipRecord{}, unsorted, nil); err != nil {
		t.Fatal(err)
	}
	_, extensions, _, _, err := readVSCodeOwnership(path)
	if err != nil {
		t.Fatal(err)
	}
	want := append([]string(nil), unsorted...)
	sort.Strings(want)
	if !reflect.DeepEqual(extensions, want) {
		t.Fatalf("round-tripped extensions = %v, want sorted %v", extensions, want)
	}

	relativeDoc := `{"version":1,"extensions":["relative/extensions/professor"]}`
	if err := os.WriteFile(path, []byte(relativeDoc), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := readVSCodeOwnership(
		path,
	); err == nil ||
		!strings.Contains(err.Error(), "invalid extension link path") {
		t.Fatalf("a relative extension path was accepted: err=%v", err)
	}

	duplicateTarget := filepath.Join(home, "dup", "extensions", "professor")
	duplicateDoc := fmt.Sprintf(`{"version":1,"extensions":[%q,%q]}`, duplicateTarget, duplicateTarget)
	if err := os.WriteFile(path, []byte(duplicateDoc), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := readVSCodeOwnership(
		path,
	); err == nil ||
		!strings.Contains(err.Error(), "duplicate extension link") {
		t.Fatalf("a duplicate extension path was accepted: err=%v", err)
	}
}

// TestVSCodeExtensionOrdinaryInstallUpgradesPreExtensionLedgerAndLinksExtension
// covers the ledger written before the extension shipped: an ordinary
// install (no --vscode) over a v1 ledger with an owned "PFM" default must
// link the extension — discovery in linkVSCodeExtension is unconditional on
// any managed run — and keep the default on the PFM settings profile.
func TestVSCodeExtensionOrdinaryInstallUpgradesPreExtensionLedgerAndLinksExtension(t *testing.T) {
	t.Setenv("VSCODE_PORTABLE", "")
	home := t.TempDir()
	settings := filepath.Join(home, "settings.json")
	writeFixture(t, settings, `{
  "terminal.integrated.profiles.linux": {
    "PFM": {"path":"/bin/zsh","args":["-l"],"env":{"PFM_AUTO_OPEN":"pfm"}}
  },
  "terminal.integrated.defaultProfile.linux": "PFM"
}`)
	writeVSCodeOwnershipFixture(t, home, vscodeOwnershipRecord{
		Path: settings, Platform: "linux", ProfileOwned: true, DefaultOwned: true,
	})

	root := filepath.Join(home, "product-a")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{}, Stdout: &bytes.Buffer{},
		vscodePlatform: "linux", vscodeSettingsPaths: []string{settings},
		vscodeExtensionRoots: []string{root},
	}); err != nil {
		t.Fatal(err)
	}

	got := readFixture(t, settings)
	if !strings.Contains(got, `"terminal.integrated.defaultProfile.linux": "PFM"`) {
		t.Fatalf("ordinary install moved the pre-extension default off the PFM settings profile:\n%s", got)
	}
	managed := filepath.Join(home, ".local", "share", "pfm", "install")
	source := filepath.Join(managed, filepath.FromSlash(vscodeExtensionSource))
	target := filepath.Join(root, "extensions", vscodeExtensionLinkName)
	resolved, linked := resolvedLink(target)
	if !linked || resolved != filepath.Clean(source) {
		t.Fatalf("ordinary install did not link the Professor extension: resolved=%q linked=%v", resolved, linked)
	}
	ledger := readVSCodeLedgerFixture(t, managed)
	if len(ledger.Extensions) != 1 || ledger.Extensions[0] != target {
		t.Fatalf("ledger extensions = %v, want [%s]", ledger.Extensions, target)
	}
}

// TestVSCodeExtensionUninstallRestoresPreviousDefaultForBothCurrentAndLegacyValue
// pins unwireVSCode's restore condition covering BOTH values a pfm-owned
// default can hold at uninstall time: "PFM" (what an install claims) and
// "Professor" (the extension's title, which the release that briefly
// selected it could have left behind).
func TestVSCodeExtensionUninstallRestoresPreviousDefaultForBothCurrentAndLegacyValue(t *testing.T) {
	for _, current := range []string{vscodeProfileName, vscodeExtensionProfileTitle} {
		t.Run(current, func(t *testing.T) {
			t.Setenv("VSCODE_PORTABLE", "")
			home := t.TempDir()
			settings := filepath.Join(home, "settings.json")
			writeFixture(t, settings, `{"terminal.integrated.defaultProfile.linux": "bash"}`)
			options := Options{
				Mode: ModeApply, Home: home, Runner: &fakeRunner{}, Stdout: &bytes.Buffer{},
				vscodePlatform: "linux", vscodeSettingsPaths: []string{settings}, VSCode: true,
			}
			if _, err := Run(context.Background(), options); err != nil {
				t.Fatal(err)
			}
			got := readFixture(t, settings)
			if !strings.Contains(got, `"terminal.integrated.defaultProfile.linux": "PFM"`) {
				t.Fatalf("install did not claim the default:\n%s", got)
			}
			overridden, err := setJSONCProperty(
				[]byte(got),
				0,
				"terminal.integrated.defaultProfile.linux",
				[]byte(`"`+current+`"`),
			)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(settings, overridden, 0o600); err != nil {
				t.Fatal(err)
			}

			options.Mode, options.VSCode = ModeUninstall, false
			if _, err := Run(context.Background(), options); err != nil {
				t.Fatal(err)
			}
			restored := readFixture(t, settings)
			if !strings.Contains(restored, `"terminal.integrated.defaultProfile.linux": "bash"`) {
				t.Fatalf("uninstall from current value %q did not restore the previous default:\n%s", current, restored)
			}
		})
	}
}

// TestVSCodeExtensionDropsARecordedTargetWhoseProductRootVanished pins the
// root os.Stat gate in linkVSCodeExtension from the OTHER side of test #1:
// a target the ledger already recorded, whose product root disappeared
// since, is dropped from the ledger by name — the root is never recreated —
// while a second, still-present recorded target survives untouched.
func TestVSCodeExtensionDropsARecordedTargetWhoseProductRootVanished(t *testing.T) {
	t.Setenv("VSCODE_PORTABLE", "")
	home := t.TempDir()
	managed := filepath.Join(home, ".local", "share", "pfm", "install")
	goneRoot := filepath.Join(home, "gone-product")
	goneTarget := filepath.Join(goneRoot, "extensions", vscodeExtensionLinkName)
	stillRoot := filepath.Join(home, "still-product")
	if err := os.MkdirAll(stillRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	stillTarget := filepath.Join(stillRoot, "extensions", vscodeExtensionLinkName)

	ledger := vscodeOwnershipDocument{Version: vscodeOwnershipVersion, Extensions: []string{goneTarget, stillTarget}}
	encoded, err := json.Marshal(ledger)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(managed, vscodeOwnershipName), string(encoded))

	installer := newVSCodeExtensionEngine(home, []string{}, false)
	if err := installer.wireVSCode(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(goneRoot); !os.IsNotExist(err) {
		t.Fatalf("a vanished product root was recreated: %v", err)
	}
	result := readVSCodeLedgerFixture(t, managed)
	if len(result.Extensions) != 1 || result.Extensions[0] != stillTarget {
		t.Fatalf("ledger extensions = %v, want only %s", result.Extensions, stillTarget)
	}
	if _, linked := resolvedLink(stillTarget); !linked {
		t.Fatal("the still-present recorded target was not (re)linked")
	}
}

// TestVSCodeExtensionPortableLinksAtPortableRootAndSettingsPathUsesUserData
// covers VSCODE_PORTABLE end to end: the extension link lands directly at
// $VSCODE_PORTABLE/extensions/professor (the portable directory IS the
// product root, unlike the fixed ~/.vscode* family), and
// vscodeSettingsPaths must offer $VSCODE_PORTABLE/user-data/User/settings.json
// — not the pre-fix "$VSCODE_PORTABLE/data/user-data/..." join.
func TestVSCodeExtensionPortableLinksAtPortableRootAndSettingsPathUsesUserData(t *testing.T) {
	home := t.TempDir()
	portable := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(portable, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VSCODE_PORTABLE", portable)

	installer := &engine{
		options: Options{
			Mode: ModeApply, Home: home, Runner: &fakeRunner{}, Stdout: &bytes.Buffer{},
			VSCode: true, vscodePlatform: "linux", vscodeSettingsPaths: []string{},
		},
		apply: true, managedRoot: filepath.Join(home, ".local", "share", "pfm", "install"), stamp: "fixture",
	}
	if err := installer.wireVSCode(); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(portable, "extensions", vscodeExtensionLinkName)
	source := filepath.Join(installer.managedRoot, filepath.FromSlash(vscodeExtensionSource))
	resolved, linked := resolvedLink(target)
	if !linked || resolved != filepath.Clean(source) {
		t.Fatalf("portable install did not link at %s: resolved=%q linked=%v", target, resolved, linked)
	}

	userDir := filepath.Join(portable, "user-data", "User")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settingsPaths := (&engine{options: Options{Home: home, vscodePlatform: "linux"}}).vscodeSettingsPaths()
	want := filepath.Join(portable, "user-data", "User", "settings.json")
	found := false
	for _, path := range settingsPaths {
		if path == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("vscodeSettingsPaths() = %v, want it to include %s", settingsPaths, want)
	}
}

// TestVSCodeExtensionPackageJSONContractMatchesTheInstalledConstantsAndStagesBothFiles
// is the cross-language contract test: it reads the embedded package.json
// through embeddedAssets — the exact FS assetFiles() walks — never a
// hardcoded host path, and checks the Go constants and the JS manifest
// agree on the profile title, the activation event names the same profile
// id it contributes, main names a file that is itself embedded, and
// assetFiles() stages both under vscode/professor/.
func TestVSCodeExtensionPackageJSONContractMatchesTheInstalledConstantsAndStagesBothFiles(t *testing.T) {
	raw, err := embeddedAssets.ReadFile("assets/" + vscodeExtensionSource + "/package.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Main             string   `json:"main"`
		ActivationEvents []string `json:"activationEvents"`
		Contributes      struct {
			Terminal struct {
				Profiles []struct {
					ID    string `json:"id"`
					Title string `json:"title"`
				} `json:"profiles"`
			} `json:"terminal"`
		} `json:"contributes"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Contributes.Terminal.Profiles) == 0 {
		t.Fatal("package.json contributes no terminal profile")
	}
	profile := manifest.Contributes.Terminal.Profiles[0]
	if profile.Title != vscodeExtensionProfileTitle {
		t.Fatalf(
			"contributed profile title = %q, want vscodeExtensionProfileTitle %q",
			profile.Title,
			vscodeExtensionProfileTitle,
		)
	}
	wantEvent := "onTerminalProfile:" + profile.ID
	found := false
	for _, event := range manifest.ActivationEvents {
		if event == wantEvent {
			found = true
		}
	}
	if !found {
		t.Fatalf("activationEvents %v missing %q", manifest.ActivationEvents, wantEvent)
	}
	mainRelative := strings.TrimPrefix(manifest.Main, "./")
	if mainRelative == "" {
		t.Fatal("package.json main is empty")
	}
	if _, err := embeddedAssets.ReadFile("assets/" + vscodeExtensionSource + "/" + mainRelative); err != nil {
		t.Fatalf("package.json main %q does not name an embedded file: %v", manifest.Main, err)
	}

	files, err := assetFiles()
	if err != nil {
		t.Fatal(err)
	}
	staged := map[string]bool{}
	for _, file := range files {
		staged[file.path] = true
	}
	for _, want := range []string{vscodeExtensionSource + "/package.json", vscodeExtensionSource + "/" + mainRelative} {
		if !staged[want] {
			t.Fatalf("assetFiles() did not stage %s; staged=%v", want, files)
		}
	}
}

// TestVSCodeExtensionCommandNeverCallsCreateTerminalWithItsOwnOptions is the
// M9 regression for issue #24 findings 10-12: professor.newChatTerminal must
// build its terminal through the SAME contributed-profile route the + dropdown
// uses (workbench.action.terminal.newWithProfile addressed at professor.terminal),
// never through a bare createTerminal(options) call, which renders the
// default profile's icon instead of the extension's own (finding 10).
func TestVSCodeExtensionCommandNeverCallsCreateTerminalWithItsOwnOptions(t *testing.T) {
	raw, err := embeddedAssets.ReadFile("assets/" + vscodeExtensionSource + "/extension.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	marker := "registerCommand('professor.newChatTerminal'"
	idx := strings.Index(source, marker)
	if idx < 0 {
		t.Fatalf("extension.js does not register professor.newChatTerminal: %s", source)
	}
	body := source[idx:]
	if strings.Contains(body, "createTerminal(") {
		t.Fatalf(
			"professor.newChatTerminal still calls createTerminal(...) with its own options instead of delegating to the contributed profile route: %s",
			body,
		)
	}
	if !strings.Contains(body, "workbench.action.terminal.newWithProfile") {
		t.Fatalf(
			"professor.newChatTerminal does not delegate through workbench.action.terminal.newWithProfile: %s",
			body,
		)
	}
	if !strings.Contains(body, "id: 'professor.terminal'") && !strings.Contains(body, `id: "professor.terminal"`) {
		t.Fatalf("professor.newChatTerminal's newWithProfile call does not address id professor.terminal: %s", body)
	}
}

// TestVSCodeExtensionContributesOneKeybindingForTheCommand is the M9
// regression for issue #24 finding 11b: pfm wires a default keybinding for
// professor.newChatTerminal so the command is reachable without the palette.
func TestVSCodeExtensionContributesOneKeybindingForTheCommand(t *testing.T) {
	raw, err := embeddedAssets.ReadFile("assets/" + vscodeExtensionSource + "/package.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Contributes struct {
			Keybindings []struct {
				Command string `json:"command"`
				Key     string `json:"key"`
				Mac     string `json:"mac"`
				When    string `json:"when"`
			} `json:"keybindings"`
		} `json:"contributes"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Contributes.Keybindings) != 1 {
		t.Fatalf("contributes.keybindings = %v, want exactly one entry", manifest.Contributes.Keybindings)
	}
	kb := manifest.Contributes.Keybindings[0]
	if kb.Command != "professor.newChatTerminal" {
		t.Fatalf("keybinding command = %q, want professor.newChatTerminal", kb.Command)
	}
	if kb.Key != "ctrl+shift+alt+t" {
		t.Fatalf("keybinding key = %q, want ctrl+shift+alt+t", kb.Key)
	}
	if kb.Mac != "cmd+shift+alt+t" {
		t.Fatalf("keybinding mac = %q, want cmd+shift+alt+t", kb.Mac)
	}
}

// TestVSCodeExtensionPreviewCreatesNoLinkAndNoLedger is the dry-run twin of
// test #1: --vscode without --yes must name the link it WOULD make without
// touching the filesystem — no symlink, no ownership ledger.
func TestVSCodeExtensionPreviewCreatesNoLinkAndNoLedger(t *testing.T) {
	t.Setenv("VSCODE_PORTABLE", "")
	home := t.TempDir()
	root := filepath.Join(home, "product-a")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	var preview bytes.Buffer
	if _, err := Run(context.Background(), Options{
		Mode: ModeDryRun, Home: home, Runner: &fakeRunner{}, Stdout: &preview, VSCode: true,
		vscodePlatform: "linux", vscodeSettingsPaths: []string{},
		vscodeExtensionRoots: []string{root},
	}); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "extensions", vscodeExtensionLinkName)
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("preview created an extension link: %v", err)
	}
	if _, err := os.Stat(
		filepath.Join(home, ".local", "share", "pfm", "install", vscodeOwnershipName),
	); !os.IsNotExist(
		err,
	) {
		t.Fatalf("preview wrote the VS Code ownership ledger: %v", err)
	}
	if !strings.Contains(preview.String(), "link "+target) {
		t.Fatalf("preview did not name the extension link it would make:\n%s", preview.String())
	}
}

// TestVSCodeSettingsMergeAndRestoreWriteThroughASymlinkedSettingsFile pins the
// dotfile-manager case: settings.json is a symlink into a repository. Renaming
// over the link would sever it — VS Code would keep reading a detached copy
// while the managed file never saw the change — so the install merge and the
// uninstall restore both write the link's target, and each backup sits beside
// that target, the way writeMCPFile already treats a linked MCP registry.
func TestVSCodeSettingsMergeAndRestoreWriteThroughASymlinkedSettingsFile(t *testing.T) {
	t.Setenv("VSCODE_PORTABLE", "")
	home := t.TempDir()
	dotfiles := filepath.Join(home, "dotfiles", "settings.json")
	link := filepath.Join(home, ".config", "Code", "User", "settings.json")
	for _, dir := range []string{filepath.Dir(dotfiles), filepath.Dir(link)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(
		dotfiles,
		[]byte("{\n  // kept by the dotfile repo\n  \"editor.tabSize\": 2\n}\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dotfiles, link); err != nil {
		t.Fatal(err)
	}
	run := func(mode Mode) {
		t.Helper()
		installer := newVSCodeExtensionEngine(home, []string{}, mode == ModeApply)
		installer.options.Mode = mode
		installer.options.vscodeSettingsPaths = []string{link}
		if err := installer.wireVSCode(); err != nil {
			t.Fatal(err)
		}
		if resolved, linked := resolvedLink(link); !linked || resolved != dotfiles {
			t.Fatalf("mode %d severed the settings link: resolved=%q linked=%v", mode, resolved, linked)
		}
	}

	run(ModeApply)
	merged, err := os.ReadFile(dotfiles)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(merged, []byte(`"terminal.integrated.defaultProfile.linux": "`+vscodeProfileName+`"`)) ||
		!bytes.Contains(merged, []byte("kept by the dotfile repo")) {
		t.Fatalf("the link's target did not receive the merge:\n%s", merged)
	}
	if backups, _ := filepath.Glob(dotfiles + ".pre-professor-*"); len(backups) != 1 {
		t.Fatalf("install backups beside the target = %v, want exactly one", backups)
	}

	run(ModeUninstall)
	restored, err := os.ReadFile(dotfiles)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(restored, []byte("terminal.integrated.defaultProfile")) ||
		bytes.Contains(restored, []byte("PFM_AUTO_OPEN")) ||
		!bytes.Contains(restored, []byte("kept by the dotfile repo")) {
		t.Fatalf("uninstall did not restore through the link:\n%s", restored)
	}
}

// TestVSCodeDefaultTerminalIsASettingsProfileNeverAnExtensionContributedOne
// pins the reload regression. VS Code rebuilds every terminal a window reload
// restores through createTerminal, and getContributedDefaultProfile hands a
// restored terminal — no executable, no extHostTerminalId — to an
// EXTENSION-contributed default profile: the extension then makes a brand-new
// terminal, the live one is never reattached, and the pty host shuts it down
// once its reconnection grace time expires. Every tab closed on reload. So the
// default pfm selects is the settings profile pfm itself writes, and an owned
// default holding the extension's contributed title (written by the release
// that briefly selected it) moves back on the next ordinary install.
func TestVSCodeDefaultTerminalIsASettingsProfileNeverAnExtensionContributedOne(t *testing.T) {
	t.Setenv("VSCODE_PORTABLE", "")
	raw, err := embeddedAssets.ReadFile("assets/" + vscodeExtensionSource + "/package.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Contributes struct {
			Terminal struct {
				Profiles []struct {
					Title string `json:"title"`
				} `json:"profiles"`
			} `json:"terminal"`
		} `json:"contributes"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	contributed := map[string]bool{}
	for _, profile := range manifest.Contributes.Terminal.Profiles {
		contributed[profile.Title] = true
	}

	home := t.TempDir()
	settings := filepath.Join(home, "settings.json")
	writeFixture(t, settings, "{}\n")
	run := func(vscodeFlag bool) (string, map[string]any) {
		t.Helper()
		installer := newVSCodeExtensionEngine(home, []string{}, vscodeFlag)
		if vscodeFlag {
			installer.options.vscodeSettingsPaths = []string{settings}
		}
		if err := installer.wireVSCode(); err != nil {
			t.Fatal(err)
		}
		content, err := os.ReadFile(settings)
		if err != nil {
			t.Fatal(err)
		}
		var document map[string]any
		if err := json.Unmarshal(content, &document); err != nil {
			t.Fatalf("settings are not strict JSON: %v\n%s", err, content)
		}
		profiles, _ := document["terminal.integrated.profiles.linux"].(map[string]any)
		value, _ := document["terminal.integrated.defaultProfile.linux"].(string)
		return value, profiles
	}
	assertSettingsProfileDefault := func(stage, value string, profiles map[string]any) {
		t.Helper()
		if _, isSettingsProfile := profiles[value]; !isSettingsProfile || contributed[value] {
			t.Fatalf(
				"%s: default terminal %q is not a settings profile pfm writes (settings profiles %v; the extension contributes %v) — a window reload would hand every restored terminal to the extension",
				stage,
				value,
				profiles,
				contributed,
			)
		}
	}

	value, profiles := run(true)
	assertSettingsProfileDefault("install", value, profiles)

	// The owned default holding the extension's title moves back.
	for title := range contributed {
		content, err := os.ReadFile(settings)
		if err != nil {
			t.Fatal(err)
		}
		writeFixture(t, settings, strings.Replace(string(content),
			`"terminal.integrated.defaultProfile.linux": "`+value+`"`,
			`"terminal.integrated.defaultProfile.linux": "`+title+`"`, 1))
		value, profiles = run(false)
		assertSettingsProfileDefault("ordinary install over an owned "+title+" default", value, profiles)
	}
}

// vscodeExtensionDriverJS stubs the 'vscode' module (Module._load
// interception, the documented way to inject a require-time fake with no
// package on disk) and drives the real embedded extension.js through
// activate() -> registerTerminalProfileProvider -> provideTerminalProfile(),
// the same call chain VS Code itself makes when a Professor terminal opens.
const vscodeExtensionDriverJS = `
const Module = require('module');
const extensionPath = process.argv[2];

const defaults = {
  shellPath: '/bin/zsh',
  shellArgs: ['-l'],
  env: { PFM_AUTO_OPEN: 'pfm' },
  icons: ['rocket'],
  colors: ['terminal.ansiRed'],
};

let provider;
const vscodeStub = {
  workspace: {
    getConfiguration() {
      return { get: (key) => defaults[key] };
    },
  },
  window: {
    onDidOpenTerminal: () => ({ dispose() {} }),
    registerTerminalProfileProvider: (id, terminalProvider) => {
      provider = terminalProvider;
      return { dispose() {} };
    },
    createTerminal: () => ({ show() {} }),
  },
  commands: { registerCommand: () => ({ dispose() {} }) },
  ThemeIcon: function (id) { this.id = id; },
  ThemeColor: function (id) { this.id = id; },
  TerminalProfile: function (options) { return options; },
};

const originalLoad = Module._load;
Module._load = function (request, parent, isMain) {
  if (request === 'vscode') return vscodeStub;
  return originalLoad.apply(this, arguments);
};

const extension = require(extensionPath);
const store = {};
const context = {
  subscriptions: [],
  globalState: {
    get: (key, def) => (key in store ? store[key] : def),
    update: (key, value) => { store[key] = value; },
  },
};
extension.activate(context);
process.stdout.write(JSON.stringify(provider.provideTerminalProfile()));
`

// TestVSCodeExtensionTerminalProfileStripsChatIdentityEnv runs the embedded
// extension.js under Node with a stubbed 'vscode' module and asserts
// nextTerminal's env carries PFM_AUTO_OPEN, a PROFESSOR_TERMINAL marker, and
// a JSON null for every chat-identity variable a launcher app could have
// inherited (see the comment beside nextTerminal's env in extension.js).
func TestVSCodeExtensionTerminalProfileStripsChatIdentityEnv(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("named gap: node unavailable; extension.js behaviour not exercised")
	}

	extensionPath, err := filepath.Abs(filepath.Join("assets", "vscode", "professor", "extension.js"))
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(extensionPath); statErr != nil {
		t.Fatalf("embedded extension asset %s: %v", extensionPath, statErr)
	}

	driverPath := filepath.Join(t.TempDir(), "driver.js")
	if err := os.WriteFile(driverPath, []byte(vscodeExtensionDriverJS), 0o600); err != nil {
		t.Fatal(err)
	}

	output, err := exec.Command(node, driverPath, extensionPath).CombinedOutput()
	if err != nil {
		t.Fatalf("run extension.js under node: %v: %s", err, output)
	}

	var profile struct {
		Env map[string]any `json:"env"`
	}
	if err := json.Unmarshal(output, &profile); err != nil {
		t.Fatalf("decode terminal profile JSON: %v: %s", err, output)
	}

	if profile.Env["PFM_AUTO_OPEN"] != "pfm" {
		t.Fatalf("terminal profile env missing PFM_AUTO_OPEN=pfm: %v", profile.Env)
	}
	marker, ok := profile.Env["PROFESSOR_TERMINAL"].(string)
	if !ok || marker == "" {
		t.Fatalf("terminal profile env missing a PROFESSOR_TERMINAL marker: %v", profile.Env)
	}
	for _, key := range []string{"CLAUDECODE", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_CHILD_SESSION", "TMUX", "TMUX_PANE"} {
		value, present := profile.Env[key]
		if !present {
			t.Fatalf("terminal profile env dropped %s entirely instead of nulling it: %v", key, profile.Env)
		}
		if value != nil {
			t.Fatalf("terminal profile env[%s] = %v, want JSON null (VS Code deletes the inherited var)", key, value)
		}
	}
}
