package installer

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallPhysicalPathStableAcrossCreation(t *testing.T) {
	root := t.TempDir()
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(realRoot, alias); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(alias, "new-account", "hooks.json")
	want := filepath.Join(realRoot, "new-account", "hooks.json")
	if got := physicalSettingsPath(path); got != want {
		t.Fatalf("before creation=%q want=%q", got, want)
	}
	writeFixture(t, path, "{}")
	if got := physicalSettingsPath(path); got != want {
		t.Fatalf("after creation=%q want=%q", got, want)
	}
}

func TestInstallHookOwnershipCanonicalizesLegacyAliases(t *testing.T) {
	root := t.TempDir()
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(realRoot, alias); err != nil {
		t.Fatal(err)
	}
	logical := filepath.Join(alias, "hooks.json")
	physical := filepath.Join(realRoot, "hooks.json")
	writeFixture(t, physical, "{}")
	key := settingsHookKey{Event: "SessionStart", Command: "managed-command"}
	ledger := filepath.Join(root, "ownership.json")
	for _, test := range []struct {
		name          string
		physicalCount int
		wantError     bool
	}{
		{"legacy only", 0, false}, {"matching alias", 1, false}, {"conflicting alias", 2, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			counts := map[string]settingsHookCounts{logical: {key: 1}}
			if test.physicalCount > 0 {
				counts[physical] = settingsHookCounts{key: test.physicalCount}
			}
			raw, err := encodeSettingsHookOwnership(counts)
			if err != nil {
				t.Fatal(err)
			}
			writeFixture(t, ledger, string(raw))
			got, _, err := readSettingsHookOwnership(ledger)
			if test.wantError {
				if err == nil {
					t.Fatal("conflicting aliases accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || len(got[physical]) != 1 || got[physical][key] != 1 {
				t.Fatalf("ownership=%v", got)
			}
		})
	}
}

func TestInstallClaudeSettingsLeafSymlinkSurvivesLifecycle(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, "account")
	target := filepath.Join(home, "personal-settings.json")
	link := filepath.Join(config, "settings.json")
	writeFixture(t, target, `{"private":"keep"}`)
	if err := os.MkdirAll(config, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	e := engine{
		options:     Options{Home: home, ConfigDirs: []string{config}, CodexHomes: []string{}, Stdout: io.Discard},
		managedRoot: filepath.Join(home, "managed"),
		apply:       true,
		stamp:       "fixture",
	}
	for _, mode := range []Mode{ModeApply, ModeUninstall} {
		e.options.Mode = mode
		if err := e.wireSettings(); err != nil {
			t.Fatal(err)
		}
		if got, err := os.Readlink(link); err != nil || got != target {
			t.Fatalf("mode=%v link=%q err=%v", mode, got, err)
		}
	}
	if !strings.Contains(readFixture(t, target), `"private": "keep"`) {
		t.Fatal("private setting lost")
	}
}
