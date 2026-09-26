package installer

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
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

func TestDedupePhysicalDirsReportsBrokenSymlinkFallbackOnceInTheTranscript(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	broken := filepath.Join(root, "broken-config")
	if err := os.Symlink(filepath.Join(root, "missing-target"), broken); err != nil {
		t.Fatal(err)
	}
	_, evalErr := filepath.EvalSymlinks(broken)
	if evalErr == nil {
		t.Fatal("broken symlink unexpectedly resolved")
	}

	var transcript bytes.Buffer
	installer := &engine{options: Options{Home: root, ConfigDir: broken, Stdout: &transcript}}
	dirs := installer.claudeConfigDirs()
	installer.claudeConfigDirs()

	if len(dirs) != 1 || dirs[0] != broken {
		t.Fatalf("claudeConfigDirs=%q, want fallback path %q", dirs, broken)
	}
	if !strings.Contains(transcript.String(), broken) || !strings.Contains(transcript.String(), evalErr.Error()) {
		t.Fatalf("transcript=%q, want path %q and full EvalSymlinks error %q", transcript.String(), broken, evalErr)
	}
	if got := strings.Count(transcript.String(), "resolve config directory"); got != 1 {
		t.Fatalf("the unresolvable config directory was reported %d times, want exactly 1", got)
	}
	if installer.report.Skipped != 1 {
		t.Fatalf("report.Skipped = %d, want the unresolved directory counted once", installer.report.Skipped)
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
