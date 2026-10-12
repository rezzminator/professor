package installer

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

func TestGptHookOwnershipRetiresHistoricalAccounts(t *testing.T) {
	installer, dirs := gptHooksEngine(t, true)
	assets, err := assetFiles()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := installer.stageAssets(assets); err != nil {
		t.Fatal(err)
	}
	installer.options.ClaudeAccounts = nil
	if err := installer.removeManagedAssets(assets); err != nil {
		t.Fatal(err)
	}
	for _, dir := range dirs {
		for _, name := range fixtureGptHooks {
			target := filepath.Join(dir, "hooks", name)
			if _, err := os.Lstat(target); !os.IsNotExist(err) {
				t.Errorf("uninstall left historical owned link %s: %v", target, err)
			}
		}
	}
}

func TestGptHookOwnershipTracksHookDirectoryAlias(t *testing.T) {
	installer, dirs := gptHooksEngine(t, true)
	shared := filepath.Join(installer.options.Home, "operator-hook-directory")
	if err := os.MkdirAll(shared, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dirs[1], 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(shared, filepath.Join(dirs[1], "hooks")); err != nil {
		t.Fatal(err)
	}
	assets, err := assetFiles()
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := installer.stageAssets(assets); err != nil {
			t.Fatalf("hook directory alias did not converge: %v", err)
		}
	}
	// A configured directory can be retargeted between successful installs.
	// Receipt history must keep the first physical hook directory too.
	moved := filepath.Join(installer.options.Home, "moved-hook-directory")
	if err := os.MkdirAll(moved, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dirs[1], "hooks")
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := installer.stageAssets(assets); err != nil {
		t.Fatalf("retargeted hook directory alias did not converge: %v", err)
	}
	installer.options.ClaudeAccounts = nil
	if err := installer.removeManagedAssets(assets); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{shared, moved} {
		for _, name := range fixtureGptHooks {
			if _, err := os.Lstat(filepath.Join(dir, name)); !os.IsNotExist(err) {
				t.Fatalf("alias left an owned hook in %s: %v", dir, err)
			}
		}
	}
	info, err := os.Lstat(filepath.Join(dirs[1], "hooks"))
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("uninstall changed operator directory alias: %v", err)
	}
}

func TestGptHookOwnershipDoesNotClaimForeignTargets(t *testing.T) {
	installer, dirs := gptHooksEngine(t, true)
	for _, dir := range dirs {
		for _, name := range fixtureGptHooks {
			writeFixture(t, filepath.Join(dir, "hooks", name), "operator script")
		}
	}
	assets, err := assetFiles()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := installer.stageAssets(assets); err == nil {
		t.Fatal("foreign hooks were not refused")
	}
	path := filepath.Join(installer.managedRoot, "gpt-hook-ownership.json")
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("failed installation recorded foreign targets: %v", err)
	}
}

func TestGptHookOwnershipDoctorReportsUnreadableReceipt(t *testing.T) {
	installer, _ := gptHooksEngine(t, true)
	assets, err := assetFiles()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := installer.stageAssets(assets); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(installer.options.Home, ".local", "bin", "pfm")
	writeFixture(t, binary, "binary")
	if err := os.Chmod(binary, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(installer.managedRoot, "gpt-hook-ownership.json")
	writeFixture(t, path, "not json")
	var output bytes.Buffer
	machine := pfmconfig.Config{Accounts: installer.options.ClaudeAccounts}
	_, failures := ReportHooks(&output, installer.options.Home, machine, false)
	if failures != 1 || !strings.Contains(output.String(), "gpt-hook-ownership.json UNREADABLE") {
		t.Fatalf("unreadable ownership receipt not reported: failures=%d output=%s", failures, output.String())
	}
}

func TestGptHookOwnershipRejectsMalformedReceipts(t *testing.T) {
	for _, receipt := range []string{"not json", `{"version":2,"targets":[]}`, `{"version":1}`, `{"version":1,"targets":["relative/hooks/gpt-return-watch.py"]}`, `{"version":1,"targets":["/fixture/operator.py"]}`} {
		for _, uninstall := range []bool{false, true} {
			t.Run(receipt+map[bool]string{false: " install", true: " uninstall"}[uninstall], func(t *testing.T) {
				installer, _ := gptHooksEngine(t, true)
				path := filepath.Join(installer.managedRoot, "gpt-hook-ownership.json")
				writeFixture(t, path, receipt)
				assets, err := assetFiles()
				if err != nil {
					t.Fatal(err)
				}
				if uninstall {
					err = installer.removeManagedAssets(assets)
				} else {
					_, err = installer.stageAssets(assets)
				}
				if err == nil || !strings.Contains(err.Error(), path) {
					t.Fatalf("malformed receipt was not visibly refused: %v", err)
				}
				if got := readFixture(t, path); got != receipt {
					t.Fatal("malformed ownership evidence was replaced")
				}
			})
		}
	}
}
