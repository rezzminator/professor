package installer

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
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
			installer.options.ConfigSeedContent = content
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

func TestSeedConfigPreservesValidatedBytesAndConcurrentTarget(t *testing.T) {
	for _, scenario := range []string{"changed example", "concurrent target"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			example := filepath.Join(home, "example.json")
			target := filepath.Join(home, "config", pfmconfig.FileName)
			content := []byte(`{"version":2,"theme":"validated"}`)
			writeFixture(t, example, string(content))
			config, err := pfmconfig.LoadSeed(example, target, home, nil)
			if err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			e := logDefaultEngine(t, target, true, &output)
			e.options.ConfigSeed = example
			e.options.ConfigSeedContent = config.SeedContent
			writeFixture(t, example, `{"version":2,"theme":"changed"}`)
			if scenario == "concurrent target" {
				writeFixture(t, target, "operator bytes")
			}
			err = e.seedConfig()
			if scenario == "concurrent target" {
				if err == nil {
					t.Error("target created meanwhile must be reported")
				}
				assertContent(t, target, "operator bytes")
			} else {
				if err != nil {
					t.Fatal(err)
				}
				assertContent(t, target, string(content))
			}
		})
	}
}

// installSeedClaude plans a first install from example (a file body) and
// returns the claude object of the bytes the apply would write, and the seed
// path.
func installSeedClaude(t *testing.T, example []byte) map[string]json.RawMessage {
	t.Helper()
	home, clone := t.TempDir(), t.TempDir()
	target := filepath.Join(home, "config", pfmconfig.FileName)
	if err := os.WriteFile(filepath.Join(clone, "example.pfm.config.json"), example, 0o600); err != nil {
		t.Fatal(err)
	}
	runtimeConfig := pfmconfig.Runtime{
		Config: pfmconfig.Config{Path: target},
		Paths: paths.Values{
			Home:  home,
			Roots: map[pfmengine.ID][]string{pfmengine.Codex: {filepath.Join(home, "codex")}},
		},
	}
	got, seeded, err := InstallConfig(runtimeConfig, &paths.MapEnv{}, clone)
	if err != nil || seeded == "" {
		t.Fatalf("InstallConfig seeded=%q err=%v", seeded, err)
	}
	// Removing default keys must not alphabetize either object in the seed.
	for _, pair := range [][2]string{{`"version"`, `"claude"`}, {`"permissionMode"`, `"binary"`}} {
		left, right := bytes.Index(got.SeedContent, []byte(pair[0])), bytes.Index(got.SeedContent, []byte(pair[1]))
		if left >= 0 && right >= 0 && left > right {
			t.Errorf("seed key order %s after %s: %s", pair[0], pair[1], got.SeedContent)
		}
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(got.SeedContent, &document); err != nil {
		t.Fatalf("seed bytes are not JSON: %v\n%s", err, got.SeedContent)
	}
	var claude map[string]json.RawMessage
	if err := json.Unmarshal(document["claude"], &claude); err != nil {
		t.Fatalf("seed claude block: %v\n%s", err, got.SeedContent)
	}
	return claude
}

// The seed a first install writes leaves out the four claude keys a pfm of the
// last release refuses (helperModel, pluginCheckoutRoot, plugins, costLedger)
// while they sit at their defaults, exactly as `pfm config init` does
// (marshalClaude); the example file itself keeps them, for its parity test.
func TestInstallConfigSeedOmitsDefaultKeysAnOlderPfmRefuses(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("find test source")
	}
	example, err := os.ReadFile(filepath.Join(filepath.Dir(source), "..", "..", "..", "example.pfm.config.json"))
	if err != nil {
		t.Fatal(err)
	}
	claude := installSeedClaude(t, example)
	for _, key := range []string{"helperModel", "pluginCheckoutRoot", "plugins", "costLedger"} {
		if _, present := claude[key]; present {
			t.Errorf("the seed keeps claude.%s at its default", key)
		}
	}
	if string(claude["permissionMode"]) != `"bypass"` || string(claude["binary"]) != `"claude"` {
		t.Errorf("the seed lost the other claude keys: %s", claude)
	}
}

// A value the example sets away from its default is kept.
func TestInstallConfigSeedKeepsNonDefaultNewKeys(t *testing.T) {
	claude := installSeedClaude(t, []byte(`{"version":2,"claude":{"helperModel":"claude-custom-1","costLedger":false,`+
		`"plugins":["agent-effort"],"theme":null}}`))
	for key, want := range map[string]string{
		"helperModel": `"claude-custom-1"`, "costLedger": "false", "plugins": `["agent-effort"]`,
	} {
		if got := strings.Join(strings.Fields(string(claude[key])), ""); got != want {
			t.Errorf("seed claude.%s = %q, want %q", key, got, want)
		}
	}
}
