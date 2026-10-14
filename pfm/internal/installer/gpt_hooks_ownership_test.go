package installer

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
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

const retiredReturnWatch = "gpt-return-watch.py"

// retiredReturnWatchHost leaves what an install that shipped the return watch
// left: a managed copy, a link per hooks directory, a log directory and,
// optionally, a receipt naming the links.
func retiredReturnWatchHost(
	t *testing.T,
	installer *engine,
	dirs []string,
	receipt bool,
) (managed, stateDir string, links []string) {
	t.Helper()
	managed = filepath.Join(installer.managedRoot, retiredReturnWatch)
	writeFixture(t, managed, "retired return watch")
	stateDir = filepath.Join(installer.options.Home, ".local", "state", "pfm", "gpt-return-watch")
	writeFixture(t, filepath.Join(stateDir, "return-watch.jsonl"), "{}\n")
	var recorded []string
	for _, dir := range dirs {
		hooks := filepath.Join(dir, "hooks")
		if err := os.MkdirAll(hooks, 0o700); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(hooks, retiredReturnWatch)
		if err := os.Symlink(managed, link); err != nil {
			t.Fatal(err)
		}
		links = append(links, link)
		recorded = append(recorded, filepath.Join(paths.PhysicalPath(hooks), retiredReturnWatch))
	}
	if receipt {
		encoded, err := json.Marshal(gptHookOwnership{Version: 1, Targets: recorded})
		if err != nil {
			t.Fatal(err)
		}
		writeFixture(t, filepath.Join(installer.managedRoot, gptHookOwnershipName), string(encoded))
	}
	return managed, stateDir, links
}

func TestGptReturnWatchRetiredHostIsSwept(t *testing.T) {
	for _, receipt := range []bool{true, false} {
		for _, uninstall := range []bool{false, true} {
			name := map[bool]string{true: "receipt", false: "no-receipt"}[receipt] + map[bool]string{false: "-install", true: "-uninstall"}[uninstall]
			t.Run(name, func(t *testing.T) {
				installer, dirs := gptHooksEngine(t, true)
				managed, stateDir, links := retiredReturnWatchHost(t, installer, dirs, receipt)
				assets, err := assetFiles()
				if err != nil {
					t.Fatal(err)
				}
				if uninstall {
					err = installer.removeManagedAssets(assets)
				} else {
					_, err = installer.stageAssets(assets)
				}
				if err != nil {
					t.Fatal(err)
				}
				for _, path := range append([]string{managed, stateDir}, links...) {
					if _, err := os.Lstat(path); !os.IsNotExist(err) {
						t.Errorf("retired return watch left %s: %v", path, err)
					}
				}
				if uninstall {
					return
				}
				targets, err := readGptHookOwnership(installer.managedRoot)
				if err != nil {
					t.Fatal(err)
				}
				if len(targets) != len(dirs) {
					t.Fatalf(
						"receipt kept %d targets, want the %d stop hook links: %v",
						len(targets),
						len(dirs),
						targets,
					)
				}
				for _, target := range targets {
					if filepath.Base(target) != "gpt-agent-stop.py" {
						t.Errorf("receipt still names %s", target)
					}
				}
				for _, dir := range dirs {
					assertLink(
						t,
						filepath.Join(dir, "hooks", "gpt-agent-stop.py"),
						filepath.Join(installer.managedRoot, "gpt-agent-stop.py"),
					)
				}
				changed := installer.report.Changed
				if _, err := installer.stageAssets(assets); err != nil {
					t.Fatal(err)
				}
				if installer.report.Changed != changed {
					t.Fatal("second install changed a swept host")
				}
			})
		}
	}
	// An account dropped from the config after the old install is no current
	// hooks directory: only the receipt still names its retired link.
	t.Run("receipt-historical-account-install", func(t *testing.T) {
		installer, dirs := gptHooksEngine(t, true)
		managed, _, links := retiredReturnWatchHost(t, installer, dirs, true)
		installer.options.ClaudeAccounts = nil
		assets, err := assetFiles()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := installer.stageAssets(assets); err != nil {
			t.Fatal(err)
		}
		for _, path := range append([]string{managed}, links...) {
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Errorf("retired return watch left %s: %v", path, err)
			}
		}
	})
}

func TestGptReturnWatchRetirementKeepsOperatorEntries(t *testing.T) {
	for _, kind := range []string{"file", "directory", "foreign-link"} {
		t.Run(kind, func(t *testing.T) {
			installer, dirs := gptHooksEngine(t, true)
			managed, stateDir, links := retiredReturnWatchHost(t, installer, dirs, true)
			operator := links[1]
			if err := os.Remove(operator); err != nil {
				t.Fatal(err)
			}
			foreign := filepath.Join(installer.options.Home, "operator.py")
			writeFixture(t, foreign, "operator bytes")
			switch kind {
			case "file":
				writeFixture(t, operator, "operator bytes")
			case "directory":
				if err := os.Mkdir(operator, 0o700); err != nil {
					t.Fatal(err)
				}
			case "foreign-link":
				if err := os.Symlink(foreign, operator); err != nil {
					t.Fatal(err)
				}
			}
			var output bytes.Buffer
			installer.options.Stdout = &output
			assets, err := assetFiles()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := installer.stageAssets(assets); err != nil {
				t.Fatalf("an operator entry at a retired hook path failed the install: %v", err)
			}
			info, err := os.Lstat(operator)
			if err != nil {
				t.Fatalf("operator entry removed: %v", err)
			}
			switch kind {
			case "file":
				if got := readFixture(t, operator); got != "operator bytes" {
					t.Fatalf("operator content changed: %q", got)
				}
			case "directory":
				if !info.IsDir() {
					t.Fatal("operator directory replaced")
				}
			case "foreign-link":
				if link, err := os.Readlink(operator); err != nil || link != foreign {
					t.Fatalf("operator link changed: %q %v", link, err)
				}
			}
			if !strings.Contains(output.String(), "preserve unowned retired gpt hook asset "+operator) {
				t.Errorf("preserved operator entry was not named: %s", output.String())
			}
			for _, link := range []string{links[0], links[2]} {
				if _, err := os.Lstat(link); !os.IsNotExist(err) {
					t.Errorf("owned retired link %s left: %v", link, err)
				}
			}
			for _, path := range []string{managed, stateDir} {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Errorf("retired return watch left %s: %v", path, err)
				}
			}
		})
	}
}

func TestGptReturnWatchRetirementPreview(t *testing.T) {
	installer, dirs := gptHooksEngine(t, false)
	managed, stateDir, links := retiredReturnWatchHost(t, installer, dirs, true)
	var output bytes.Buffer
	installer.options.Stdout = &output
	assets, err := assetFiles()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := installer.stageAssets(assets); err != nil {
		t.Fatal(err)
	}
	for _, path := range append([]string{managed, stateDir}, links...) {
		if _, err := os.Lstat(path); err != nil {
			t.Errorf("preview removed %s: %v", path, err)
		}
		if !strings.Contains(output.String(), path) {
			t.Errorf("preview did not name %s", path)
		}
	}
}

func TestGptReturnWatchReceiptEntryIsNotADoctorRow(t *testing.T) {
	installer, dirs := gptHooksEngine(t, true)
	retiredReturnWatchHost(t, installer, dirs, true)
	for _, dir := range dirs {
		for _, name := range fixtureGptHooks {
			content, err := readAsset(name)
			if err != nil {
				t.Fatal(err)
			}
			writeFixture(t, filepath.Join(dir, "hooks", name), string(content))
		}
	}
	binary := filepath.Join(installer.options.Home, ".local", "bin", "pfm")
	writeFixture(t, binary, "binary")
	if err := os.Chmod(binary, 0o700); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	machine := pfmconfig.Config{Accounts: installer.options.ClaudeAccounts}
	_, failures := ReportHooks(&output, installer.options.Home, machine, false)
	if failures != 0 {
		t.Fatalf("a pre-sweep receipt failed the doctor: %d: %s", failures, output.String())
	}
	if rows := strings.Count(output.String(), " asset "); rows != len(dirs) {
		t.Fatalf("doctor rows=%d want=%d: %s", rows, len(dirs), output.String())
	}
}
