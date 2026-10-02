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
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestInstallHostGateBlocksBeforeAnyWrite(t *testing.T) {
	for _, args := range [][]string{{"--yes"}, {}, {"--check"}} {
		t.Run(fmt.Sprint(args), func(t *testing.T) {
			home, clone, _ := installCheckHome(t, true)
			// A config seed would write here if it ever preceded the gate.
			if err := os.WriteFile(
				filepath.Join(clone, "example.pfm.config.json"),
				[]byte(`{"version":2,"theme":"seeded"}`),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
			legacyDir := pfmconfig.LegacyConfigDir(paths.OSEnv{}, home)
			if err := os.WriteFile(
				filepath.Join(legacyDir, "config.json"),
				[]byte(`{"version":2}`),
				0o600,
			); err != nil {
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
			legacy := filepath.Join(pfmconfig.LegacyConfigDir(paths.OSEnv{}, home), pfmconfig.FileName)
			want := ""
			// Each BLOCK row carries its fix: a rolled-back binary's doctor may have no host checks.
			for _, path := range []string{legacy, filepath.Join(legacyDir, "config.json")} {
				want += "pfm install: BLOCK legacy-config " + path + " — legacy pfm config outside the clone\n" +
					"pfm install:   fix: mv " + path + " " + loaded.Config.Path + "\n"
			}
			want += "pfm install: 2 blocking — run pfm doctor for the fixes\n"
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
	home, _, _ := installCheckHome(t, false)
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
		[]string{"--yes", "--skip-harvest"},
		&out,
		&errOut,
		loaded,
	); code != 0 || !called ||
		!strings.HasPrefix(out.String(), "pfm install: 1 warnings — run pfm doctor to see them\n") {
		t.Fatalf("code=%d called=%t stdout=%q stderr=%q", code, called, out.String(), errOut.String())
	}
}

func TestInstallFirstConfigSeed(t *testing.T) {
	for _, apply := range []bool{false, true} {
		t.Run(fmt.Sprint(apply), func(t *testing.T) {
			home, clone, _ := installCheckHome(t, false)
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
				if apply {
					if info, err := os.Stat(target); err != nil || info.Mode().Perm() != 0o600 {
						t.Fatalf("seed missing before install: %v", err)
					}
				}
				return installer.Report{}, nil
			}
			before := installCheckTree(t, home)
			args := []string{"--skip-harvest"}
			if apply {
				args = append(args, "--yes")
			}
			var out, errOut bytes.Buffer
			if code := runInstall(args, &out, &errOut, loaded); code != 0 {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
			}
			if apply {
				if !strings.Contains(out.String(), "  change  seed "+target+" from "+example+"\n") {
					t.Fatal(out.String())
				}
			} else if !reflect.DeepEqual(before, installCheckTree(t, home)) {
				t.Fatal("preview seeded home")
			}
		})
	}
}

func TestInstallSeedErrorNamesContext(t *testing.T) {
	for _, scenario := range []string{"preview read", "apply read", "apply write"} {
		t.Run(scenario, func(t *testing.T) {
			home, clone, _ := installCheckHome(t, false)
			if err := os.Remove(filepath.Join(clone, pfmconfig.FileName)); err != nil {
				t.Fatal(err)
			}
			example := filepath.Join(clone, "example.pfm.config.json")
			if scenario == "apply write" {
				if err := os.WriteFile(example, []byte(`{"version":2}`), 0o600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Mkdir(example, 0o700); err != nil {
				t.Fatal(err)
			}
			loaded, err := pfmconfig.LoadInstallRuntime("")
			if err != nil {
				t.Fatal(err)
			}
			want := "pfm install: seed config: read install example "
			if scenario == "apply write" {
				parent := filepath.Join(home, "dangling-config")
				if err := os.Symlink(filepath.Join(home, "absent"), parent); err != nil {
					t.Fatal(err)
				}
				loaded.Config.Path = filepath.Join(parent, pfmconfig.FileName)
				want = "pfm install: seed config: write install config "
			}
			args := []string{"--skip-harvest"}
			if scenario != "preview read" {
				args = append(args, "--yes")
			}
			var out, errOut bytes.Buffer
			if code := runInstall(args, &out, &errOut, loaded); code != 1 || !strings.HasPrefix(errOut.String(), want) {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
			}
		})
	}
}
