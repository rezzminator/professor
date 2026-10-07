package installer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallPhysicalPathStableAcrossCreation(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
