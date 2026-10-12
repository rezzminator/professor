package installer

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

var fixtureGptHooks = []string{"gpt-return-watch.py", "gpt-agent-stop.py"}

func stageGptHookProbeAssets(t *testing.T, dir string) {
	t.Helper()
	for _, name := range fixtureGptHooks {
		content, err := readAsset(name)
		if err != nil {
			t.Fatal(err)
		}
		writeFixture(t, filepath.Join(dir, "hooks", name), string(content))
	}
}

func gptHooksEngine(t *testing.T, apply bool) (*engine, []string) {
	t.Helper()
	home := t.TempDir()
	dirs := []string{filepath.Join(home, ".claude"), filepath.Join(home, "account-a"), filepath.Join(home, "account-b")}
	return &engine{
		options: Options{
			Home: home, ConfigDir: dirs[0], Stdout: io.Discard,
			ClaudeAccounts: []pfmconfig.Account{{ID: 1, ConfigDir: dirs[1]}, {ID: 2, ConfigDir: dirs[2]}},
		},
		managedRoot: managedRootForHome(home), apply: apply,
	}, dirs
}

func TestGptHookAssetsInstallRepairAndPreview(t *testing.T) {
	t.Run("relative-explicit-store", func(t *testing.T) {
		installer, _ := gptHooksEngine(t, true)
		t.Chdir(installer.options.Home)
		installer.options.ConfigDir = "custom-claude"
		assets, err := assetFiles()
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if _, err := installer.stageAssets(assets); err != nil {
				t.Fatalf("relative explicit store did not converge: %v", err)
			}
		}
		if err := installer.removeManagedAssets(assets); err != nil {
			t.Fatal(err)
		}
		for _, name := range fixtureGptHooks {
			if _, err := os.Lstat(
				filepath.Join(installer.options.Home, "custom-claude", "hooks", name),
			); !os.IsNotExist(
				err,
			) {
				t.Fatalf("relative explicit store left owned hook: %v", err)
			}
		}
	})
	for _, apply := range []bool{false, true} {
		t.Run(map[bool]string{false: "preview", true: "apply"}[apply], func(t *testing.T) {
			installer, dirs := gptHooksEngine(t, apply)
			var output bytes.Buffer
			installer.options.Stdout = &output
			assets, err := assetFiles()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := installer.stageAssets(assets); err != nil {
				t.Fatal(err)
			}
			for _, dir := range dirs {
				for _, name := range fixtureGptHooks {
					target := filepath.Join(dir, "hooks", name)
					if !apply {
						if !strings.Contains(output.String(), target) {
							t.Errorf("preview did not name %s", target)
						}
						if _, err := os.Lstat(target); !os.IsNotExist(err) {
							t.Errorf("preview wrote %s: %v", target, err)
						}
						continue
					}
					assertLink(t, target, filepath.Join(installer.managedRoot, name))
					want, err := readAsset(name)
					if err != nil {
						t.Fatal(err)
					}
					if got := readFixture(t, target); got != string(want) {
						t.Errorf("installed %s differs from asset", target)
					}
				}
			}
			if !apply {
				return
			}
			unchanged := installer.report.Changed
			if _, err := installer.stageAssets(assets); err != nil {
				t.Fatal(err)
			}
			if installer.report.Changed != unchanged {
				t.Fatal("second install rewrote healthy hooks")
			}
			missing := filepath.Join(dirs[1], "hooks", fixtureGptHooks[0])
			if err := os.Remove(missing); err != nil {
				t.Fatal(err)
			}
			writeFixture(t, filepath.Join(installer.managedRoot, fixtureGptHooks[1]), "changed owned asset")
			if _, err := installer.stageAssets(assets); err != nil {
				t.Fatal(err)
			}
			assertLink(t, missing, filepath.Join(installer.managedRoot, fixtureGptHooks[0]))
			want, err := readAsset(fixtureGptHooks[1])
			if err != nil {
				t.Fatal(err)
			}
			if got := readFixture(t, filepath.Join(dirs[2], "hooks", fixtureGptHooks[1])); got != string(want) {
				t.Fatal("changed owned asset was not repaired")
			}
		})
	}
}

func TestGptHookAssetsPreserveUnownedTargets(t *testing.T) {
	for _, kind := range []string{"file", "directory", "foreign-link"} {
		t.Run(kind, func(t *testing.T) {
			installer, dirs := gptHooksEngine(t, true)
			target := filepath.Join(dirs[1], "hooks", fixtureGptHooks[0])
			foreign := filepath.Join(installer.options.Home, "operator.py")
			writeFixture(t, foreign, "operator bytes")
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "file":
				writeFixture(t, target, "operator bytes")
			case "directory":
				if err := os.Mkdir(target, 0o700); err != nil {
					t.Fatal(err)
				}
			case "foreign-link":
				if err := os.Symlink(foreign, target); err != nil {
					t.Fatal(err)
				}
			}
			assets, err := assetFiles()
			if err != nil {
				t.Fatal(err)
			}
			_, err = installer.stageAssets(assets)
			if err == nil || !strings.Contains(err.Error(), target) || !strings.Contains(err.Error(), "unowned") {
				t.Fatalf("unowned %s was not visibly refused: %v", target, err)
			}
			if kind == "directory" {
				info, err := os.Lstat(target)
				if err != nil || !info.IsDir() {
					t.Fatalf("operator directory replaced: %v", err)
				}
			} else if got := readFixture(t, target); got != "operator bytes" {
				t.Fatalf("operator content changed: %q", got)
			}
			targets, err := readGptHookOwnership(installer.managedRoot)
			if err != nil {
				t.Fatal(err)
			}
			if len(targets) != len(dirs)*len(fixtureGptHooks)-1 {
				t.Fatalf("partial install recorded %d targets: %v", len(targets), targets)
			}
			for _, recorded := range targets {
				if recorded == target {
					t.Fatal("partial install claimed the operator entry")
				}
			}
			installer.options.ClaudeAccounts = nil
			if err := installer.removeManagedAssets(assets); err != nil {
				t.Fatal(err)
			}
			for _, recorded := range targets {
				if _, err := os.Lstat(recorded); !os.IsNotExist(err) {
					t.Fatalf("partial uninstall left owned target %s: %v", recorded, err)
				}
			}
			if kind == "directory" {
				info, err := os.Lstat(target)
				if err != nil || !info.IsDir() {
					t.Fatalf("uninstall replaced operator directory: %v", err)
				}
			} else if got := readFixture(t, target); got != "operator bytes" {
				t.Fatalf("uninstall changed operator content: %q", got)
			}
		})
	}
}

func TestGptHookAssetsUninstallOnlyOwnedLinks(t *testing.T) {
	installer, dirs := gptHooksEngine(t, true)
	assets, err := assetFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range dirs {
		for _, name := range fixtureGptHooks {
			source := filepath.Join(installer.managedRoot, name)
			writeFixture(t, source, "owned fixture")
			target := filepath.Join(dir, "hooks", name)
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(source, target); err != nil {
				t.Fatal(err)
			}
		}
	}
	foreign := filepath.Join(dirs[2], "hooks", fixtureGptHooks[1])
	if err := os.Remove(foreign); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, foreign, "operator hook")
	if err := installer.removeManagedAssets(assets); err != nil {
		t.Fatal(err)
	}
	for _, dir := range dirs {
		for _, name := range fixtureGptHooks {
			target := filepath.Join(dir, "hooks", name)
			if target == foreign {
				continue
			}
			if _, err := os.Lstat(target); !os.IsNotExist(err) {
				t.Errorf("uninstall retained owned hook %s: %v", target, err)
			}
		}
	}
	if got := readFixture(t, foreign); got != "operator hook" {
		t.Fatalf("uninstall changed operator hook: %q", got)
	}
}

func TestReportHooksGptHookAssets(t *testing.T) {
	for _, location := range []string{"default-no-accounts", "default-no-receipt", "explicit-store", "removed-account", "hooks-alias"} {
		t.Run(location, func(t *testing.T) {
			installer, dirs := gptHooksEngine(t, true)
			machine := pfmconfig.Config{Accounts: installer.options.ClaudeAccounts}
			targetDir := filepath.Join(dirs[0], "hooks")
			wantRows := 6
			switch location {
			case "default-no-accounts", "default-no-receipt":
				installer.options.ClaudeAccounts = nil
				machine.Accounts = nil
				wantRows = 2
			case "explicit-store":
				installer.options.ConfigDir = filepath.Join(installer.options.Home, "explicit-store")
				installer.options.ClaudeAccounts = nil // --config-dir does not populate this field.
				targetDir = filepath.Join(installer.options.ConfigDir, "hooks")
				wantRows = 8
				for _, dir := range dirs[1:] {
					stageGptHookProbeAssets(t, dir)
				}
			case "removed-account":
				machine.Accounts = nil
				targetDir = filepath.Join(dirs[1], "hooks")
			case "hooks-alias":
				targetDir = filepath.Join(installer.options.Home, "operator-hook-directory")
				if err := os.MkdirAll(targetDir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(dirs[1], 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(targetDir, filepath.Join(dirs[1], "hooks")); err != nil {
					t.Fatal(err)
				}
			}
			assets, err := assetFiles()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := installer.stageAssets(assets); err != nil {
				t.Fatal(err)
			}
			if location == "default-no-receipt" {
				if err := os.Remove(filepath.Join(installer.managedRoot, gptHookOwnershipName)); err != nil {
					t.Fatal(err)
				}
			}
			binary := filepath.Join(installer.options.Home, ".local", "bin", "pfm")
			writeFixture(t, binary, "binary")
			if err := os.Chmod(binary, 0o700); err != nil {
				t.Fatal(err)
			}
			for _, missing := range []bool{false, true} {
				target := filepath.Join(targetDir, fixtureGptHooks[0])
				wantState, wantFailures := "ok", 0
				if missing {
					if err := os.Remove(target); err != nil {
						t.Fatal(err)
					}
					wantState, wantFailures = "MISSING", 1
				}
				var output bytes.Buffer
				_, failures := ReportHooks(&output, installer.options.Home, machine, false)
				rows, named := 0, false
				for _, line := range strings.Split(output.String(), "\n") {
					if strings.Contains(line, " asset ") {
						rows++
					}
					if strings.Contains(line, targetDir) && strings.Contains(line, fixtureGptHooks[0]) &&
						strings.Contains(line, wantState) {
						named = true
					}
				}
				if rows != wantRows || failures != wantFailures || !named {
					t.Errorf("doctor enumeration missing=%v rows=%d want=%d failures=%d want=%d target=%s state=%s: %s",
						missing, rows, wantRows, failures, wantFailures, target, wantState, output.String())
				}
			}
		})
	}
	for _, state := range []string{"missing", "unreadable", "healthy", "changed"} {
		t.Run(state, func(t *testing.T) {
			installer, dirs := gptHooksEngine(t, true)
			binary := filepath.Join(installer.options.Home, ".local", "bin", "pfm")
			writeFixture(t, binary, "binary")
			if err := os.Chmod(binary, 0o700); err != nil {
				t.Fatal(err)
			}
			machine := pfmconfig.Config{Accounts: installer.options.ClaudeAccounts}
			if state != "missing" {
				for _, dir := range dirs {
					for _, name := range fixtureGptHooks {
						target := filepath.Join(dir, "hooks", name)
						switch state {
						case "unreadable":
							if err := os.MkdirAll(target, 0o700); err != nil {
								t.Fatal(err)
							}
						case "changed":
							writeFixture(t, target, "stale owned fixture")
						default:
							content, err := readAsset(name)
							if err != nil {
								t.Fatal(err)
							}
							writeFixture(t, target, string(content))
						}
					}
				}
			}
			var output bytes.Buffer
			_, failures := ReportHooks(&output, installer.options.Home, machine, false)
			want := map[string]string{"missing": "MISSING", "unreadable": "UNREADABLE", "healthy": "ok", "changed": "DRIFT"}[state]
			for _, dir := range dirs {
				for _, name := range fixtureGptHooks {
					found := false
					for _, line := range strings.Split(output.String(), "\n") {
						if strings.Contains(line, name) && strings.Contains(line, dir) && strings.Contains(line, want) {
							found = true
						}
					}
					if !found {
						t.Errorf("doctor did not name %s in %s as %s: %s", name, dir, want, output.String())
					}
				}
			}
			wantFailures := len(dirs) * len(fixtureGptHooks)
			if state == "healthy" {
				wantFailures = 0
			}
			if failures != wantFailures {
				t.Fatalf("failures=%d want=%d: %s", failures, wantFailures, output.String())
			}
		})
	}
}
