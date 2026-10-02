package installer

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestInstallConfig(t *testing.T) {
	for _, scenario := range []string{"existing", "empty target", "explicit missing", "no clone", "no example", "preview", "apply", "marker wins"} {
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
			apply := scenario == "apply"
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
			case "preview":
				wantTheme = "seeded"
			case "apply":
				wantTheme, wantSeed = "seeded", example
			case "marker wins":
				if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
					t.Fatal(err)
				}
				clone = t.TempDir()
				wantTheme = "seeded"
			}
			before := snapshotMemoryMigrationTree(t, home)
			got, seeded, err := InstallConfig(runtime, &paths.MapEnv{}, clone, apply)
			if err != nil || got.Theme != wantTheme || seeded != wantSeed {
				t.Fatalf("config=%+v seeded=%q err=%v", got, seeded, err)
			}
			if !apply && !reflect.DeepEqual(before, snapshotMemoryMigrationTree(t, home)) {
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
			if apply {
				raw, err := os.ReadFile(target)
				if err != nil || !bytes.Equal(raw, content) {
					t.Fatalf("bytes=%q err=%v", raw, err)
				}
				info, err := os.Stat(target)
				if err != nil || info.Mode().Perm() != 0o600 {
					t.Fatalf("mode=%v err=%v", info, err)
				}
			}
		})
	}
}

func TestInstallConfigErrors(t *testing.T) {
	for _, scenario := range []string{"inspect target", "load existing", "marker", "read example", "preview load", "apply load", "write target", "resolve clone"} {
		t.Run(scenario, func(t *testing.T) {
			home, clone := t.TempDir(), t.TempDir()
			target := filepath.Join(home, "config", pfmconfig.FileName)
			example := filepath.Join(clone, "example.pfm.config.json")
			if err := os.WriteFile(example, []byte(`{"version":2}`), 0o600); err != nil {
				t.Fatal(err)
			}
			apply := scenario == "write target" || scenario == "apply load"
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
			case "preview load", "apply load":
				if err := os.WriteFile(example, []byte("invalid"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "write target":
				if err := os.Symlink(filepath.Join(home, "absent"), filepath.Dir(target)); err != nil {
					t.Fatal(err)
				}
			case "resolve clone":
				cwd := t.TempDir()
				t.Chdir(cwd)
				if err := os.Remove(cwd); err != nil {
					t.Fatal(err)
				}
				clone = "relative"
			}
			runtime := pfmconfig.Runtime{Config: pfmconfig.Config{Path: target}, Paths: paths.Values{Home: home}}
			if _, _, err := InstallConfig(runtime, &paths.MapEnv{}, clone, apply); err == nil {
				t.Fatal("unreadable config returned success")
			}
		})
	}
}
