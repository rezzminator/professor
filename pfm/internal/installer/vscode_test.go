package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVSCodeTerminalProfileIsPreviewedMergedIdempotentAndReversed(t *testing.T) {
	home := t.TempDir()
	settings := filepath.Join(home, ".config", "Code", "User", "settings.json")
	original := `{
  // operator comment must survive PFM's merge
  "editor.fontSize": 16,
  "terminal.integrated.profiles.linux": {
    // existing profiles belong to the operator
    "zsh": {"path": "/bin/zsh"},
  },
  "terminal.integrated.defaultProfile.linux": "zsh",
}
`
	writeFixture(t, settings, original)

	options := Options{
		Home: home, Runner: &fakeRunner{}, VSCode: true,
		vscodePlatform: "linux", vscodeSettingsPaths: []string{settings},
	}
	var preview bytes.Buffer
	options.Mode, options.Stdout = ModeDryRun, &preview
	if report, err := Run(context.Background(), options); err != nil || report.Changed == 0 {
		t.Fatalf("preview report=%#v err=%v\n%s", report, err, preview.String())
	}
	if !strings.Contains(preview.String(), "merge VS Code PFM terminal profile "+settings) {
		t.Fatalf("preview did not name the exact VS Code settings mutation:\n%s", preview.String())
	}
	if got := readFixture(t, settings); got != original {
		t.Fatalf("preview changed settings.json:\n%s", got)
	}

	options.Mode, options.Stdout = ModeApply, &bytes.Buffer{}
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	merged := readFixture(t, settings)
	for _, want := range []string{
		"// operator comment must survive PFM's merge",
		"// existing profiles belong to the operator",
		`"zsh": {"path": "/bin/zsh"}`,
		`"PFM":`,
		`"PFM_AUTO_OPEN": "pfm"`,
		`"terminal.integrated.defaultProfile.linux": "PFM"`,
		// The terminal-persistence keys: tmux is the chat's survival layer,
		// the tab only a view — persistence reconnects the view across a
		// reload, revive stays "never" so a dead server does not spawn one
		// live picker per tab, and the exit alert / port-forward keys stay
		// off (see vscodeOwnershipRecord.ScalarOwned).
		`"terminal.integrated.enablePersistentSessions": true`,
		`"terminal.integrated.persistentSessionReviveProcess": "never"`,
		`"terminal.integrated.showExitAlert": false`,
		`"remote.autoForwardPorts": false`,
	} {
		if !strings.Contains(merged, want) {
			t.Errorf("merged settings missing %q:\n%s", want, merged)
		}
	}
	if _, err := decodeJSONCObject([]byte(merged)); err != nil {
		t.Fatalf("merged settings are not valid JSONC: %v\n%s", err, merged)
	}

	beforeSecond := merged
	options.VSCode = false // the ownership ledger keeps updates reconciled.
	if report, err := Run(context.Background(), options); err != nil || report.Changed != 0 {
		t.Fatalf("second apply report=%#v err=%v", report, err)
	}
	if got := readFixture(t, settings); got != beforeSecond {
		t.Fatalf("idempotent apply rewrote settings.json:\n%s", got)
	}

	options.Mode = ModeUninstall
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	restored := readFixture(t, settings)
	for _, want := range []string{
		"// operator comment must survive PFM's merge",
		"// existing profiles belong to the operator",
		`"zsh": {"path": "/bin/zsh"}`,
		`"terminal.integrated.defaultProfile.linux": "zsh"`,
	} {
		if !strings.Contains(restored, want) {
			t.Errorf("restored settings missing %q:\n%s", want, restored)
		}
	}
	if strings.Contains(restored, `"PFM"`) || strings.Contains(restored, "PFM_AUTO_OPEN") {
		t.Fatalf("uninstall retained the PFM profile:\n%s", restored)
	}
	for _, gone := range []string{
		"enablePersistentSessions",
		"persistentSessionReviveProcess",
		"terminal.integrated.showExitAlert",
		"remote.autoForwardPorts",
	} {
		if strings.Contains(restored, gone) {
			t.Fatalf("uninstall retained %q (settings.json had no such key before install):\n%s", gone, restored)
		}
	}
	if _, err := os.Stat(
		filepath.Join(home, ".local", "share", "pfm", "install", vscodeOwnershipName),
	); !os.IsNotExist(
		err,
	) {
		t.Fatalf("uninstall retained VS Code ownership ledger: %v", err)
	}
}

func TestVSCodeTerminalProfileRefusesAnOperatorProfileWithTheSameName(t *testing.T) {
	home := t.TempDir()
	settings := filepath.Join(home, "settings.json")
	original := `{"terminal.integrated.profiles.linux":{"PFM":{"path":"/operator/shell"}}}`
	writeFixture(t, settings, original)

	_, err := Run(context.Background(), Options{
		Mode: ModeDryRun, Home: home, Runner: &fakeRunner{}, VSCode: true,
		vscodePlatform: "linux", vscodeSettingsPaths: []string{settings},
	})
	if err == nil || !strings.Contains(err.Error(), `profile "PFM" already exists and is not PFM-owned`) {
		t.Fatalf("error=%v, want an exact-name profile conflict", err)
	}
	if got := readFixture(t, settings); got != original {
		t.Fatalf("conflict changed settings.json: %q", got)
	}
}

func TestVSCodeProfileConflictRefusesApplyBeforeInstallerWrites(t *testing.T) {
	home := t.TempDir()
	settings := filepath.Join(home, "settings.json")
	original := `{"terminal.integrated.profiles.linux":{"PFM":{"path":"/operator/shell"}}}`
	writeFixture(t, settings, original)

	_, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{}, VSCode: true,
		vscodePlatform: "linux", vscodeSettingsPaths: []string{settings},
	})
	if err == nil || !strings.Contains(err.Error(), `profile "PFM" already exists and is not PFM-owned`) {
		t.Fatalf("apply error=%v, want preflight profile conflict", err)
	}
	if got := readFixture(t, settings); got != original {
		t.Fatalf("conflicting apply changed settings.json: %q", got)
	}
	for _, path := range []string{
		filepath.Join(home, ".zshrc"),
		filepath.Join(home, ".local", "share", "pfm", "install"),
	} {
		if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
			t.Fatalf("preflight conflict wrote %s: %v", path, statErr)
		}
	}
}

func TestVSCodeUninstallPreservesAnOperatorOverrideAfterInstall(t *testing.T) {
	home := t.TempDir()
	settings := filepath.Join(home, "settings.json")
	writeFixture(t, settings, `{}`)
	options := Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{}, VSCode: true,
		vscodePlatform: "linux", vscodeSettingsPaths: []string{settings},
	}
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	raw := []byte(readFixture(t, settings))
	overridden, err := setJSONCProperty(raw, 0, "terminal.integrated.defaultProfile.linux", []byte(`"bash"`))
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
	got := readFixture(t, settings)
	if !strings.Contains(got, `"terminal.integrated.defaultProfile.linux": "bash"`) {
		t.Fatalf("uninstall clobbered the operator's post-install default:\n%s", got)
	}
}

// TestVSCodeScalarKeyOperatorOverrideSurvivesUninstall mirrors
// TestVSCodeUninstallPreservesAnOperatorOverrideAfterInstall for the four
// scalar keys ScalarOwned added alongside DefaultOwned: an operator turning
// the exit alert back on after install is an intentional override, and
// uninstall must leave it exactly as the operator left it rather than
// restoring pfm's own remembered prior state.
func TestVSCodeScalarKeyOperatorOverrideSurvivesUninstall(t *testing.T) {
	home := t.TempDir()
	settings := filepath.Join(home, "settings.json")
	writeFixture(t, settings, `{}`)
	options := Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{}, VSCode: true,
		vscodePlatform: "linux", vscodeSettingsPaths: []string{settings},
	}
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if got := readFixture(t, settings); !strings.Contains(got, `"terminal.integrated.showExitAlert": false`) {
		t.Fatalf("install did not set showExitAlert:\n%s", got)
	}

	raw := []byte(readFixture(t, settings))
	overridden, err := setJSONCProperty(raw, 0, "terminal.integrated.showExitAlert", []byte("true"))
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
	got := readFixture(t, settings)
	if !strings.Contains(got, `"terminal.integrated.showExitAlert": true`) {
		t.Fatalf("uninstall clobbered the operator's post-install showExitAlert override:\n%s", got)
	}
}

func TestVSCodeUninstallRemovesASettingsFilePFMCreated(t *testing.T) {
	home := t.TempDir()
	settings := filepath.Join(home, ".config", "Code", "User", "settings.json")
	options := Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{}, VSCode: true,
		vscodePlatform: "linux", vscodeSettingsPaths: []string{settings},
	}
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(settings); err != nil {
		t.Fatalf("install did not create settings: %v", err)
	}
	options.Mode, options.VSCode = ModeUninstall, false
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(settings); !os.IsNotExist(err) {
		t.Fatalf("uninstall retained the settings file PFM created: %v", err)
	}
	if backups, _ := filepath.Glob(settings + ".pre-professor-*"); len(backups) != 0 {
		t.Fatalf("uninstall backed up an installer-created settings file: %v", backups)
	}
}

func TestVSCodeProfileUsesTheShimPickerValueExplicitly(t *testing.T) {
	shim := readFixture(t, filepath.Join("assets", "shim", "pfm.zsh"))
	if !strings.Contains(shim, `cmd="$HOME/.local/bin/pfm"`) {
		t.Fatal("the installed PFM_AUTO_OPEN=pfm value is not routed to the absolute PFM picker")
	}
}

func TestVSCodeDarwinUsesTheOSXTerminalKeysAndUserSettingsPath(t *testing.T) {
	home := t.TempDir()
	settings := filepath.Join(home, "Library", "Application Support", "Code", "User", "settings.json")
	paths := (&engine{options: Options{Home: home, vscodePlatform: "darwin"}}).vscodeSettingsPaths()
	if len(paths) != 1 || paths[0] != settings {
		t.Fatalf("darwin settings paths=%q, want %q", paths, settings)
	}
	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{}, VSCode: true,
		vscodePlatform: "darwin", vscodeSettingsPaths: []string{settings},
	}); err != nil {
		t.Fatal(err)
	}
	got := readFixture(t, settings)
	for _, want := range []string{
		`"terminal.integrated.profiles.osx"`,
		`"terminal.integrated.defaultProfile.osx": "PFM"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("darwin settings missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "profiles.linux") || strings.Contains(got, "defaultProfile.linux") {
		t.Fatalf("darwin settings contain Linux keys:\n%s", got)
	}
}

func TestVSCodeNewPathUsesLivePlatformNotAnOlderRecordsPlatform(t *testing.T) {
	home := t.TempDir()
	managed := filepath.Join(home, ".local", "share", "pfm", "install")
	oldPath := filepath.Join(home, "a-old-settings.json")
	newPath := filepath.Join(home, "z-new-settings.json")
	profile, err := json.Marshal(vscodeProfile())
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(
		t,
		oldPath,
		`{"terminal.integrated.profiles.osx":{"PFM":`+string(
			profile,
		)+`},"terminal.integrated.defaultProfile.osx":"PFM"}`,
	)
	record := vscodeOwnershipDocument{Version: vscodeOwnershipVersion, Files: []vscodeOwnershipRecord{{
		Path: oldPath, Platform: "osx", ProfileOwned: true, DefaultOwned: true,
	}}}
	ledger, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(managed, vscodeOwnershipName), string(ledger))
	writeFixture(t, newPath, `{}`)
	installer := engine{
		options: Options{
			Mode: ModeApply, Home: home, VSCode: true, Stdout: &bytes.Buffer{},
			vscodePlatform: "linux", vscodeSettingsPaths: []string{newPath},
		},
		apply: true, managedRoot: managed, stamp: "fixture",
	}
	if err := installer.wireVSCode(); err != nil {
		t.Fatal(err)
	}
	got := readFixture(t, newPath)
	if !strings.Contains(got, `"terminal.integrated.profiles.linux"`) || strings.Contains(got, `profiles.osx`) {
		t.Fatalf("new VS Code path inherited stale record platform:\n%s", got)
	}
}

func TestVSCodeEditedProfileSurvivesUninstallAndDoesNotBlockReinstall(t *testing.T) {
	home := t.TempDir()
	settings := filepath.Join(home, "settings.json")
	writeFixture(t, settings, `{}`)
	options := Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{}, VSCode: true,
		vscodePlatform: "linux", vscodeSettingsPaths: []string{settings},
	}
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(readFixture(t, settings), `"path": "/bin/zsh"`, `"path": "/operator/zsh"`, 1)
	if err := os.WriteFile(settings, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	options.Mode, options.VSCode = ModeUninstall, false
	var removed bytes.Buffer
	options.Stdout = &removed
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readFixture(t, settings), `/operator/zsh`) {
		t.Fatal("uninstall removed the operator-edited PFM profile")
	}
	ownership := filepath.Join(home, ".local", "share", "pfm", "install", vscodeOwnershipName)
	if _, err := os.Stat(ownership); err != nil {
		t.Fatalf("uninstall dropped recovery ownership for edited profile: %v\n%s", err, removed.String())
	}

	options.Mode, options.VSCode = ModeApply, true
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatalf("reinstall after edited-profile refusal: %v", err)
	}
	got := readFixture(t, settings)
	if !strings.Contains(got, `/operator/zsh`) ||
		!strings.Contains(got, `"terminal.integrated.defaultProfile.linux": "PFM"`) {
		t.Fatalf("reinstall did not reconcile around retained edited profile:\n%s", got)
	}
}

func TestMalformedVSCodeSettingsSkipsVisiblyWithoutBlockingInstall(t *testing.T) {
	home := t.TempDir()
	settings := filepath.Join(home, "settings.json")
	writeFixture(t, settings, "{broken\n")
	var output bytes.Buffer
	_, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{}, VSCode: true, Stdout: &output,
		vscodePlatform: "linux", vscodeSettingsPaths: []string{settings},
	})
	if err != nil {
		t.Fatalf("malformed VS Code settings blocked unrelated install steps: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "skip") || !strings.Contains(output.String(), "VS Code") ||
		!strings.Contains(output.String(), "decode") {
		t.Fatalf("malformed VS Code settings were not reported visibly:\n%s", output.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".zshrc")); err != nil {
		t.Fatalf("install did not complete shell wiring after VS Code skip: %v", err)
	}
	if got := readFixture(t, settings); got != "{broken\n" {
		t.Fatalf("malformed settings were rewritten: %q", got)
	}
}

// realMalformedVSCodeSettings is a byte-for-byte copy of the live
// ~/.vscode-server/data/Machine/settings.json backup taken on devbox
// 2026-09-03 (settings.json.bak-20260903, 1733 bytes): a hand-inserted PFM
// block left the "devbox (zsh)" profile's closing brace followed by a
// comma on its own line (an orphaned separator, harmless) and then
// terminal.integrated.profiles.linux itself closing with `},\n  },` — a
// trailing comma before ITS OWN closing brace. errors.Is(err,
// errMalformedVSCodeSettings) is what this used to trip.
const realMalformedVSCodeSettings = `{
  "terminal.integrated.profiles.linux": {
    "bash": {
      "path": "/usr/bin/bash"
    },
    "zsh": {
      "path": "/usr/bin/zsh"
    },
    "tmux": {
      "path": "/usr/local/bin/tmux",
      "icon": "terminal-tmux"
    },
    "tmux + claude": {
      "path": "/usr/bin/zsh",
      "env": {
        "VSCODE_AUTO_CC": "1"
      }
    },
    "devbox-2 (tmux)": {
      "path": "/usr/local/bin/tmux",
      "args": [
        "-L",
        "vscode",
        "new-session",
        "-A",
        "-s",
        "main"
      ],
      "icon": "server-environment"
    },
    "devbox-2 (zsh)": {
      "path": "/usr/bin/zsh",
      "icon": "terminal"
    },
    "vsct": {
      "path": "/opt/ops/scripts/vsct.sh",
      "icon": "server-process",
      "env": {
        "VSCODE_AUTO_CC": "1"
      }
    },
    "devbox (tmux)": {
      "path": "/usr/local/bin/tmux",
      "args": [
        "-L",
        "vscode",
        "new-session",
        "-A",
        "-s",
        "main"
      ],
      "icon": "server-environment"
    },
    "devbox (zsh)": {
      "path": "/usr/bin/zsh",
      "icon": "terminal"
    }
  ,
    "PFM": {
      "args": [
        "-l"
      ],
      "env": {
		"PFM_AUTO_OPEN": "pfm"
      },
      "path": "/bin/zsh"
    },
  },
  "terminal.integrated.defaultProfile.linux": "PFM",
  "terminal.integrated.automationProfile.linux": {
    "path": "/usr/bin/zsh"
  },
  "terminal.integrated.tabs.title": "${sequence}",
  "remote.autoForwardPorts": false,
  "terminal.integrated.enablePersistentSessions": true,
  "terminal.integrated.persistentSessionReviveProcess": "never",
  "terminal.integrated.persistentSessionScrollback": 2000,
  "terminal.integrated.showExitAlert": false
}
`

// TestVSCodeMergeToleratesTheRealMalformedTrailingCommaFile proves
// sanitizeJSONC/parseJSONCObject's trailing-comma tolerance against the
// actual file that tripped errMalformedVSCodeSettings on devbox, not a
// synthetic stand-in: the merge must proceed (no skip, no error). The PFM
// profile, the "PFM" default, and all four ScalarOwned keys already match
// what pfm would write (an "ok", not a "change" — same law as every other
// already-correct settings file) —
// TestVSCodeMergeWritesStrictJSONIntoTheMalformedProfilesObject is the
// sibling fixture that forces an actual write to the PROFILE object and
// proves THAT output is strict-JSON-clean. What this test proves is
// JSONC tolerance: the merge reads the malformed file successfully
// (decodeJSONCObject, the same tolerant parser wireVSCode uses), and the PFM
// profile, all four ScalarOwned keys, and everything the operator's file
// already carried — the other profiles, the automation profile, the tab
// title — read back correctly.
func TestVSCodeMergeToleratesTheRealMalformedTrailingCommaFile(t *testing.T) {
	home := t.TempDir()
	settings := filepath.Join(home, "settings.json")
	writeFixture(t, settings, realMalformedVSCodeSettings)

	var output bytes.Buffer
	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{}, VSCode: true, Stdout: &output,
		vscodePlatform: "linux", vscodeSettingsPaths: []string{settings},
	}); err != nil {
		t.Fatalf("merge refused the real malformed file: %v\n%s", err, output.String())
	}
	if strings.Contains(output.String(), "VS Code settings skipped") {
		t.Fatalf("merge skipped the real malformed file instead of tolerating it:\n%s", output.String())
	}

	got := readFixture(t, settings)
	if _, err := decodeJSONCObject([]byte(got)); err != nil {
		t.Fatalf("result does not parse as JSONC: %v\n%s", err, got)
	}
	for _, want := range []string{
		`"PFM":`,
		`"PFM_AUTO_OPEN": "pfm"`,
		`"terminal.integrated.defaultProfile.linux": "PFM"`,
		`"terminal.integrated.enablePersistentSessions": true`,
		`"terminal.integrated.persistentSessionReviveProcess": "never"`,
		`"terminal.integrated.showExitAlert": false`,
		`"remote.autoForwardPorts": false`,
		// the operator's own content, untouched by the merge
		`"bash": {`,
		`"vsct": {`,
		`"terminal.integrated.automationProfile.linux"`,
		`"terminal.integrated.tabs.title": "${sequence}"`,
		`"terminal.integrated.persistentSessionScrollback": 2000`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("result missing %q:\n%s", want, got)
		}
	}
}

// TestVSCodeMergeWritesStrictJSONIntoTheMalformedProfilesObject is the
// insert-shaped twin of the fixture above: strip the PFM profile out of the
// same real malformed file, leaving profiles.linux malformed with no PFM
// entry to converge on — exactly the state a broken hand-edit that never
// successfully added PFM would leave, and the one shape that forces pfm to
// actually parse into and WRITE a new property inside the malformed nested
// object (setJSONCProperty's insert path), not just decide nothing had
// changed. The result must still be valid strict JSON at that object.
func TestVSCodeMergeWritesStrictJSONIntoTheMalformedProfilesObject(t *testing.T) {
	home := t.TempDir()
	settings := filepath.Join(home, "settings.json")
	withoutPFM := strings.Replace(realMalformedVSCodeSettings, `    "PFM": {
      "args": [
        "-l"
      ],
      "env": {
		"PFM_AUTO_OPEN": "pfm"
      },
      "path": "/bin/zsh"
    },
`, "", 1)
	if withoutPFM == realMalformedVSCodeSettings {
		t.Fatal("fixture setup did not find the PFM block to strip — the constant above drifted")
	}
	writeFixture(t, settings, withoutPFM)

	var output bytes.Buffer
	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{}, VSCode: true, Stdout: &output,
		vscodePlatform: "linux", vscodeSettingsPaths: []string{settings},
	}); err != nil {
		t.Fatalf("merge refused the malformed file: %v\n%s", err, output.String())
	}
	if strings.Contains(output.String(), "VS Code settings skipped") {
		t.Fatalf("merge skipped the malformed file instead of tolerating it:\n%s", output.String())
	}

	got := readFixture(t, settings)
	var strict map[string]any
	if err := json.Unmarshal([]byte(got), &strict); err != nil {
		t.Fatalf("result is not valid strict JSON: %v\n%s", err, got)
	}
	for _, want := range []string{
		`"PFM":`,
		`"PFM_AUTO_OPEN": "pfm"`,
		`"bash": {`,
		`"devbox (zsh)": {`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("result missing %q:\n%s", want, got)
		}
	}
}

func TestVSCodeMergePreservesExistingSettingsMode(t *testing.T) {
	home := t.TempDir()
	settings := filepath.Join(home, "settings.json")
	writeFixture(t, settings, `{}`)
	if err := os.Chmod(settings, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{}, VSCode: true,
		vscodePlatform: "linux", vscodeSettingsPaths: []string{settings},
	}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(settings)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("VS Code merge changed settings mode to %o, want 644", got)
	}
}
