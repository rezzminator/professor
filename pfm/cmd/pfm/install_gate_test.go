package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
)

func TestInstallHostGateBlocksBeforeAnyWrite(t *testing.T) {
	for _, args := range [][]string{{"--yes"}, {}, {"--check"}} {
		t.Run(fmt.Sprint(args), func(t *testing.T) {
			home, clone, account := installCheckHome(t)
			// No clone config: a config seed would write one if it ever
			// preceded the gate.
			if err := os.Remove(filepath.Join(clone, pfmconfig.FileName)); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(
				filepath.Join(clone, "example.pfm.config.json"),
				[]byte(`{"version":2,"theme":"seeded"}`),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
			identity := filepath.Join(installer.ClaudeStore(home), ".credentials.json")
			if err := os.MkdirAll(filepath.Dir(identity), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(identity, []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
			stale := filepath.Join(home, ".claude.json.tmp.fixture")
			if err := os.WriteFile(stale, []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
			old := time.Now().Add(-48 * time.Hour)
			if err := os.Chtimes(stale, old, old); err != nil {
				t.Fatal(err)
			}
			loaded, err := pfmconfig.LoadInstallRuntime("")
			if err != nil {
				t.Fatal(err)
			}
			saved := runInstaller
			t.Cleanup(func() { runInstaller = saved })
			runInstaller = func(context.Context, installer.Options) (installer.Report, error) {
				t.Fatal("blocked gate ran installer")
				return installer.Report{}, nil
			}
			before := installCheckTree(t, home)
			var out, errOut bytes.Buffer
			code := runInstall(args, &out, &errOut, loaded)
			// The BLOCK row carries its fix: a rolled-back binary's doctor may have no host checks.
			want := "pfm install: BLOCK store-identity " + identity + " — .credentials.json is account identity inside the store, " +
				"written by a Claude launched with CLAUDE_CONFIG_DIR set to the store (a loop over config dirs that still lists it)\n" +
				"pfm install:   fix: mkdir -m 700 -p " + account + " && mv " + identity + " " + filepath.Join(
				account,
				".credentials.json",
			) + "\n" +
				"pfm install: 1 blocking — run pfm doctor for the fixes\n"
			if code != 4 || out.Len() != 0 || errOut.String() != want {
				t.Fatalf("code=%d stdout=%q stderr=%q want=%q", code, out.String(), errOut.String(), want)
			}
			if !reflect.DeepEqual(before, installCheckTree(t, home)) {
				t.Fatal("blocking gate changed home")
			}
		})
	}
}

func TestInstallHostGateWarningsProceed(t *testing.T) {
	home, _, _ := installCheckHome(t)
	stale := filepath.Join(home, ".claude.json.tmp.fixture")
	if err := os.WriteFile(stale, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	registry := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(
		registry,
		[]byte(
			`{"mcpServers":{"professor":{"type":"stdio","command":"`+filepath.Join(
				home,
				".local",
				"bin",
				"pfm",
			)+`","args":["mcp","serve","--stdio"]}}}`,
		),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	loaded, err := pfmconfig.LoadInstallRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	saved := runInstaller
	t.Cleanup(func() { runInstaller = saved })
	called := false
	runInstaller = func(_ context.Context, options installer.Options) (installer.Report, error) {
		called = true
		if options.Mode != installer.ModeApply {
			t.Errorf("mode=%v", options.Mode)
		}
		return installer.Report{}, nil
	}
	var out, errOut bytes.Buffer
	if code := runInstall(
		[]string{"--yes", "--offline"},
		&out,
		&errOut,
		loaded,
	); code != 0 || !called ||
		!strings.HasPrefix(out.String(), "pfm install: 3 warnings — run pfm doctor to see them\n") {
		t.Fatalf("code=%d called=%t stdout=%q stderr=%q", code, called, out.String(), errOut.String())
	}
}

func TestInstallFirstConfigSeed(t *testing.T) {
	for _, apply := range []bool{false, true} {
		t.Run(fmt.Sprint(apply), func(t *testing.T) {
			home, clone, _ := installCheckHome(t)
			target := filepath.Join(clone, pfmconfig.FileName)
			if err := os.Remove(target); err != nil {
				t.Fatal(err)
			}
			example := filepath.Join(clone, "example.pfm.config.json")
			if err := os.WriteFile(
				example,
				[]byte(`{"version":2,"theme":"seeded","mcp":{"http":{"port":19444}}}`),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
			loaded, err := pfmconfig.LoadInstallRuntime("")
			if err != nil {
				t.Fatal(err)
			}
			saved := runInstaller
			t.Cleanup(func() { runInstaller = saved })
			runInstaller = func(_ context.Context, options installer.Options) (installer.Report, error) {
				if options.MCPPort != 19444 {
					t.Errorf("port=%d, want seeded 19444", options.MCPPort)
				}
				if options.MCPConfigPath != target {
					t.Errorf("config=%q", options.MCPConfigPath)
				}
				if options.ConfigSeed != example {
					t.Errorf("seed=%q, want %q", options.ConfigSeed, example)
				}
				if _, err := os.Stat(target); !os.IsNotExist(err) {
					t.Fatalf("command wrote seed before engine: %v", err)
				}
				return installer.Report{}, nil
			}
			before := installCheckTree(t, home)
			args := []string{"--offline"}
			if apply {
				args = append(args, "--yes")
			}
			var out, errOut bytes.Buffer
			if code := runInstall(args, &out, &errOut, loaded); code != 0 {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
			}
			if strings.Contains(out.String(), "  change  seed ") {
				t.Fatalf("command printed engine seed row: %s", out.String())
			}
			if !reflect.DeepEqual(before, installCheckTree(t, home)) {
				t.Fatal("command changed home before engine")
			}
		})
	}
}

func TestInstallSeedErrorNamesContext(t *testing.T) {
	for _, scenario := range []string{"preview read", "apply read"} {
		t.Run(scenario, func(t *testing.T) {
			_, clone, _ := installCheckHome(t)
			if err := os.Remove(filepath.Join(clone, pfmconfig.FileName)); err != nil {
				t.Fatal(err)
			}
			example := filepath.Join(clone, "example.pfm.config.json")
			if err := os.Mkdir(example, 0o700); err != nil {
				t.Fatal(err)
			}
			loaded, err := pfmconfig.LoadInstallRuntime("")
			if err != nil {
				t.Fatal(err)
			}

			args := []string{"--offline"}
			if scenario != "preview read" {
				args = append(args, "--yes")
			}
			var out, errOut bytes.Buffer
			code := runInstall(args, &out, &errOut, loaded)
			if got, want := rooted(
				errOut.String(),
				clone,
			), "pfm install: seed config: read install example {root}/example.pfm.config.json: read {root}/example.pfm.config.json: is a directory\n"; code != 1 ||
				got != want {
				t.Fatalf("code=%d stdout=%q stderr = %q, want %q", code, out.String(), got, want)
			}
		})
	}
}

func TestInstallRosterConfigDirsWithConfigDirFlag(t *testing.T) {
	home, clone, _ := installCheckHome(t)
	account := filepath.Join(home, "acct1")
	if err := os.Mkdir(account, 0o700); err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf(`{"version":2,"accounts":[{"id":1,"configDir":%q}]}`, account)
	if err := os.WriteFile(filepath.Join(clone, pfmconfig.FileName), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := pfmconfig.LoadInstallRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	saved := runInstaller
	t.Cleanup(func() { runInstaller = saved })
	called := false
	runInstaller = func(_ context.Context, options installer.Options) (installer.Report, error) {
		called = true
		if !reflect.DeepEqual(options.RosterConfigDirs, []string{account}) || options.ClaudeAccounts != nil {
			t.Errorf("roster=%v ClaudeAccounts=%v", options.RosterConfigDirs, options.ClaudeAccounts)
		}
		return installer.Report{}, nil
	}
	var out, errOut bytes.Buffer
	if code := runInstall(
		[]string{"--yes", "--offline", "--config-dir", account},
		&out,
		&errOut,
		loaded,
	); code != 0 ||
		!called {
		t.Fatalf("code=%d called=%t stdout=%q stderr=%q", code, called, out.String(), errOut.String())
	}
}
