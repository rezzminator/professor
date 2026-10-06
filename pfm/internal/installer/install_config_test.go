package installer

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestInstallConfig(t *testing.T) {
	for _, scenario := range []string{"existing", "empty target", "explicit missing", "no clone", "no example", "plan seed", "marker wins"} {
		t.Run(scenario, func(t *testing.T) {
			home, clone := t.TempDir(), t.TempDir()
			target := filepath.Join(home, "config", pfmconfig.FileName)
			original := pfmconfig.Config{Path: target, Theme: "original"}
			runtime := pfmconfig.Runtime{
				Config: original,
				Paths: paths.Values{
					Home:  home,
					Roots: map[pfmengine.ID][]string{pfmengine.Codex: {filepath.Join(home, "codex")}},
				},
			}
			example := filepath.Join(clone, "example.pfm.config.json")
			content := []byte(`{"version":2,"theme":"seeded"}`)
			if err := os.WriteFile(example, content, 0o600); err != nil {
				t.Fatal(err)
			}
			wantTheme, wantSeed := "original", ""
			switch scenario {
			case "existing":
				if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(target, []byte(`{"version":2,"theme":"existing"}`), 0o600); err != nil {
					t.Fatal(err)
				}
				wantTheme = "existing"
			case "empty target":
				runtime.Config.Path = ""
			case "explicit missing":
				runtime.ConfigExplicit = true
			case "no clone":
				clone = ""
			case "no example":
				if err := os.Remove(example); err != nil {
					t.Fatal(err)
				}
			case "plan seed":
				wantTheme, wantSeed = "seeded", example
			case "marker wins":
				if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
					t.Fatal(err)
				}
				clone = t.TempDir()
				wantTheme, wantSeed = "seeded", example
			}
			before := snapshotMemoryMigrationTree(t, home)
			got, seeded, err := InstallConfig(runtime, &paths.MapEnv{}, clone)
			if err != nil || got.Theme != wantTheme || seeded != wantSeed {
				t.Fatalf("config=%+v seeded=%q err=%v", got, seeded, err)
			}
			if !reflect.DeepEqual(before, snapshotMemoryMigrationTree(t, home)) {
				t.Fatal("read-only config changed home")
			}
			if wantTheme == "original" && !reflect.DeepEqual(got, runtime.Config) {
				t.Fatal("fallback changed runtime config")
			}
			if wantTheme == "seeded" {
				if got.Path != target || got.Harvester.Path != pfmconfig.HarvesterPath(target) {
					t.Fatalf("paths=%q/%q", got.Path, got.Harvester.Path)
				}
				if len(got.Accounts) == 0 || got.Accounts[0].ConfigDir != pfmconfig.DefaultAccountDir(home, 1) {
					t.Fatalf("accounts=%+v", got.Accounts)
				}
			}
		})
	}
}

func TestInstallConfigErrors(t *testing.T) {
	for _, scenario := range []string{"inspect target", "load existing", "marker", "read example", "preview load", "resolve clone", "file clone"} {
		t.Run(scenario, func(t *testing.T) {
			home, clone := t.TempDir(), t.TempDir()
			target := filepath.Join(home, "config", pfmconfig.FileName)
			example := filepath.Join(clone, "example.pfm.config.json")
			if err := os.WriteFile(example, []byte(`{"version":2}`), 0o600); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "inspect target":
				if err := os.WriteFile(filepath.Dir(target), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			case "load existing":
				if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(target, []byte("invalid"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "marker":
				if err := os.MkdirAll(paths.SourceRepoPath(home), 0o700); err != nil {
					t.Fatal(err)
				}
			case "read example":
				if err := os.Remove(example); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(example, 0o700); err != nil {
					t.Fatal(err)
				}
			case "preview load":
				if err := os.WriteFile(example, []byte("invalid"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "file clone":
				clone = example
			case "resolve clone":
				cwd := t.TempDir()
				t.Chdir(cwd)
				if err := os.Remove(cwd); err != nil {
					t.Fatal(err)
				}
				clone = "relative"
			}
			runtime := pfmconfig.Runtime{Config: pfmconfig.Config{Path: target}, Paths: paths.Values{Home: home}}
			_, _, err := InstallConfig(runtime, &paths.MapEnv{}, clone)
			if err == nil {
				t.Fatal("unreadable config returned success")
			}
			if (scenario == "resolve clone" || scenario == "file clone") &&
				!strings.HasPrefix(err.Error(), "resolve install clone "+strconv.Quote(clone)+": ") {
				t.Fatalf("clone error = %v", err)
			}
		})
	}
}

func TestSeedConfig(t *testing.T) {
	for _, scenario := range []string{"dry run", "apply", "nothing", "write fails"} {
		t.Run(scenario, func(t *testing.T) {
			home, clone := t.TempDir(), t.TempDir()
			target := filepath.Join(home, "config", pfmconfig.FileName)
			example := filepath.Join(clone, "example.pfm.config.json")
			content := []byte(`{"version":2,"theme":"seeded"}`)
			writeFixture(t, example, string(content))
			var output bytes.Buffer
			installer := logDefaultEngine(t, target, scenario != "dry run", &output)
			installer.options.ConfigSeed = example
			if scenario == "nothing" {
				installer.options.ConfigSeed = ""
			}
			if scenario == "write fails" {
				if err := os.Symlink(filepath.Join(home, "absent"), filepath.Dir(target)); err != nil {
					t.Fatal(err)
				}
			}
			err := installer.seedConfig()
			if scenario == "write fails" {
				if err == nil || !strings.HasPrefix(err.Error(), "write install config "+target+": ") {
					t.Fatalf("seed write error = %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			want, changed := "  change  seed "+target+" from "+example+"\n", 1
			if scenario == "nothing" {
				want, changed = "", 0
			}
			if output.String() != want || installer.report.Changed != changed {
				t.Fatalf("output=%q changed=%d, want %q/%d", output.String(), installer.report.Changed, want, changed)
			}
			if scenario == "apply" {
				raw, err := os.ReadFile(target)
				if err != nil || !bytes.Equal(raw, content) {
					t.Fatalf("bytes=%q err=%v", raw, err)
				}
				info, err := os.Stat(target)
				if err != nil || info.Mode().Perm() != 0o600 {
					t.Fatalf("mode=%v err=%v", info, err)
				}
			} else if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Fatalf("target exists: %v", err)
			}
		})
	}
}
