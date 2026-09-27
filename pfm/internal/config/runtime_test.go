package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestResolveDevVersion(t *testing.T) {
	const fullRevision = "8f9b8bb29513ff82f0ce31d5fc4547f9e30b7071"
	cases := []struct {
		name     string
		settings []debug.BuildSetting
		want     string
	}{
		{name: "no settings", settings: nil, want: "dev"},
		{
			name:     "vcs present but no revision key",
			settings: []debug.BuildSetting{{Key: "vcs", Value: "git"}},
			want:     "dev",
		},
		{
			name: "clean checkout",
			settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: fullRevision},
				{Key: "vcs.modified", Value: "false"},
			},
			want: "dev (8f9b8bb29513)",
		},
		{
			name: "modified checkout",
			settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: fullRevision},
				{Key: "vcs.modified", Value: "true"},
			},
			want: "dev (8f9b8bb29513, modified)",
		},
		{
			name: "short revision",
			settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "8f9b8bb"},
				{Key: "vcs.modified", Value: "false"},
			},
			want: "dev (8f9b8bb)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveDevVersion(tc.settings); got != tc.want {
				t.Fatalf("ResolveDevVersion(%v) = %q, want %q", tc.settings, got, tc.want)
			}
		})
	}
}

func TestDisplayVersionPrefersLdflagsStamp(t *testing.T) {
	if got := DisplayVersion("v0.67.0"); got != "v0.67.0" {
		t.Fatalf("DisplayVersion() = %q, want the ldflags-stamped version unchanged", got)
	}
}

func brokenConfig(t *testing.T) string {
	t.Helper()
	t.Setenv(paths.EnvHome, t.TempDir())
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("this is [not a config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestLoadRuntimeRefusesABrokenConfig pins that the ordinary loader never runs
// on a config it could not read: a command acting on defaults the operator did
// not choose is worse than one that stops.
func TestLoadRuntimeRefusesABrokenConfig(t *testing.T) {
	if _, err := LoadRuntime(brokenConfig(t)); err == nil {
		t.Fatal("LoadRuntime() accepted a broken config")
	}
}

// TestLoadDiagnosticRuntimeRunsOnDefaultsAndCarriesTheError pins the diagnostic
// contract: usable on a broken config, and the error still reaches the command.
func TestLoadDiagnosticRuntimeRunsOnDefaultsAndCarriesTheError(t *testing.T) {
	path := brokenConfig(t)
	runtime, err := LoadDiagnosticRuntime(path)
	if err != nil {
		t.Fatalf("LoadDiagnosticRuntime() = %v", err)
	}
	if runtime.ConfigError == nil {
		t.Fatal("ConfigError is nil: the broken config vanished from the command surface")
	}
	if runtime.Config.Path != path || !runtime.Config.Exists || len(runtime.Config.Accounts) == 0 {
		t.Fatalf("Config = %+v, want defaults stamped with the broken path", runtime.Config)
	}
	if runtime.Paths.Home != os.Getenv(paths.EnvHome) {
		t.Fatalf("Paths.Home = %q, want the jail home", runtime.Paths.Home)
	}
}

// Codex roots follow the account roster while Claude roots retain the
// transcript store resolved by paths.Resolve.
func TestLoadRuntimePointsTheEngineRootsAtTheRoster(t *testing.T) {
	t.Setenv(paths.EnvHome, t.TempDir())
	runtime, err := LoadRuntime(filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil {
		t.Fatalf("LoadRuntime() = %v", err)
	}
	if got, want := runtime.Paths.Roots[pfmengine.Codex], runtime.Config.CodexHomes(); len(got) != len(want) ||
		(len(got) != 0 && got[0] != want[0]) {
		t.Fatalf("codex roots = %v, want the roster's homes %v", got, want)
	}
	if runtime.ConfigError != nil {
		t.Fatalf("ConfigError = %v on an absent config", runtime.ConfigError)
	}
}

func TestLoadRuntimeUsesSharedClaudeRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv(paths.EnvHome, home)
	t.Setenv(paths.EnvClaudeRoots, "")
	configPath := filepath.Join(home, "config.json")
	content := fmt.Sprintf(
		`{"version":2,"accounts":[{"id":1,"configDir":%q},{"id":2,"configDir":%q},{"id":3,"configDir":%q}]}`,
		filepath.Join(home, ".cc", "1"),
		filepath.Join(home, ".cc", "2"),
		filepath.Join(home, ".cc", "3"),
	)
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime, err := LoadRuntime(configPath)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".claude", "projects")
	if got := runtime.Paths.Roots[pfmengine.Claude]; len(got) != 1 || got[0] != want {
		t.Fatalf("Claude roots = %q, want [%q]", got, want)
	}
}

func TestLoadRuntimePreservesClaudeJailRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv(paths.EnvHome, home)
	a, b := filepath.Join(home, "a"), filepath.Join(home, "b")
	t.Setenv(paths.EnvClaudeRoots, a+string(os.PathListSeparator)+b)
	configPath := filepath.Join(home, "config.json")
	content := fmt.Sprintf(
		`{"version":2,"accounts":[{"id":1,"configDir":%q},{"id":2,"configDir":%q}]}`,
		filepath.Join(home, ".cc", "1"),
		filepath.Join(home, ".cc", "2"),
	)
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime, err := LoadRuntime(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := runtime.Paths.Roots[pfmengine.Claude]; len(got) != 2 || got[0] != a || got[1] != b {
		t.Fatalf("Claude roots = %q, want [%q %q]", got, a, b)
	}
}

// TestLoadRuntimeRecordsWhetherConfigWasExplicit pins issue #24's missing
// non-default config guard while treating a named spelling of the resolved
// default path (the clone's pfm.config.json) the same as an omitted flag.
func TestLoadRuntimeRecordsWhetherConfigWasExplicit(t *testing.T) {
	home := t.TempDir()
	t.Setenv(paths.EnvHome, home)
	t.Setenv(paths.EnvConfig, "")
	repo := filepath.Join(home, "pfm")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := paths.WriteSourceRepoMarker(home, repo); err != nil {
		t.Fatal(err)
	}
	implicit, err := LoadRuntime("")
	if err != nil {
		t.Fatalf("LoadRuntime(\"\") = %v", err)
	}
	if implicit.ConfigExplicit {
		t.Fatalf("LoadRuntime(\"\").ConfigExplicit = true, want false for the default location")
	}
	defaultPath, err := ResolvePath(home)
	if err != nil {
		t.Fatalf("ResolvePath(%q) = %v", home, err)
	}
	namedDefault := filepath.Dir(defaultPath) + "/../pfm/" + filepath.Base(defaultPath)
	named, err := LoadRuntime(namedDefault)
	if err != nil {
		t.Fatalf("LoadRuntime(%q) = %v", namedDefault, err)
	}
	if named.ConfigExplicit {
		t.Fatalf("LoadRuntime(%q).ConfigExplicit = true, want false for the default location", namedDefault)
	}

	// A relative spelling of the default, resolved against the working directory.
	t.Chdir(home)
	relativeDefault, err := filepath.Rel(home, defaultPath)
	if err != nil {
		t.Fatal(err)
	}
	relative, err := LoadRuntime(relativeDefault)
	if err != nil {
		t.Fatalf("LoadRuntime(%q) = %v", relativeDefault, err)
	}
	if relative.ConfigExplicit {
		t.Fatalf("LoadRuntime(%q).ConfigExplicit = true, want false for the default location", relativeDefault)
	}

	explicitPath := filepath.Join(t.TempDir(), "explicit.json")
	explicit, err := LoadRuntime(explicitPath)
	if err != nil {
		t.Fatalf("LoadRuntime(%q) = %v", explicitPath, err)
	}
	if !explicit.ConfigExplicit {
		t.Fatalf("LoadRuntime(%q).ConfigExplicit = false, want true for a named path", explicitPath)
	}
}

// TestLoadRuntimeDefaultUnderAConfigOverrideIsNotExplicit pins
// pfm-update-2#F29's override half: with PFM_CONFIG set, the default is the
// override — naming it is not explicit, and naming the clone's spelling (no
// longer the default) is.
func TestLoadRuntimeDefaultUnderAConfigOverrideIsNotExplicit(t *testing.T) {
	home := t.TempDir()
	t.Setenv(paths.EnvHome, home)
	repo := filepath.Join(home, "pfm")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := paths.WriteSourceRepoMarker(home, repo); err != nil {
		t.Fatal(err)
	}
	override := filepath.Join(t.TempDir(), FileName)
	t.Setenv(paths.EnvConfig, override)
	if got, err := ResolvePath(home); err != nil || got != override {
		t.Fatalf("ResolvePath under %s = %q, %v; want %q", paths.EnvConfig, got, err, override)
	}
	named, err := LoadRuntime(override)
	if err != nil {
		t.Fatalf("LoadRuntime(%q) = %v", override, err)
	}
	if named.ConfigExplicit {
		t.Fatalf("LoadRuntime(%q).ConfigExplicit = true, want false for the override default", override)
	}
	cloneSpelling := filepath.Join(repo, FileName)
	other, err := LoadRuntime(cloneSpelling)
	if err != nil {
		t.Fatalf("LoadRuntime(%q) = %v", cloneSpelling, err)
	}
	if !other.ConfigExplicit {
		t.Fatalf("LoadRuntime(%q).ConfigExplicit = false, want true: it is not the override default", cloneSpelling)
	}
}

// TestLoadRuntimeLegacyDefaultIsNotExplicit pins a4d89776 across the move of
// the config into the clone: an older pfm update names its own default, the
// legacy config directory, and that spelling is not explicit — under an
// XDG_CONFIG_HOME override too — while another file there is.
func TestLoadRuntimeLegacyDefaultIsNotExplicit(t *testing.T) {
	home := t.TempDir()
	t.Setenv(paths.EnvHome, home)
	t.Setenv(paths.EnvConfig, "")
	xdg := filepath.Join(t.TempDir(), "xdg")
	for _, tc := range []struct {
		name, xdg, dir string
	}{
		{"home", "", filepath.Join(home, ".config", "pfm")},
		{"xdg", xdg, filepath.Join(xdg, "pfm")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", tc.xdg)
			for _, name := range []string{FileName, LegacyFileName} {
				legacy := filepath.Join(tc.dir, name)
				runtime, err := LoadRuntime(legacy)
				if err != nil {
					t.Fatalf("LoadRuntime(%q) = %v", legacy, err)
				}
				if runtime.ConfigExplicit {
					t.Fatalf("LoadRuntime(%q).ConfigExplicit = true, want false for the legacy default", legacy)
				}
			}
			other := filepath.Join(tc.dir, "other.json")
			runtime, err := LoadRuntime(other)
			if err != nil {
				t.Fatalf("LoadRuntime(%q) = %v", other, err)
			}
			if !runtime.ConfigExplicit {
				t.Fatalf("LoadRuntime(%q).ConfigExplicit = false, want true", other)
			}
		})
	}
}

// A jailed override is writable, while a home without a marker has no default writer target.
func TestConfigInitWritesInsideTheJailAndRefusesMissingSourceRepo(t *testing.T) {
	home := t.TempDir()
	t.Setenv(paths.EnvHome, home)
	path := filepath.Join(home, "pfm.config.json")
	t.Setenv(paths.EnvConfig, path)
	runtime, err := LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	if runtime.Config.Path != path {
		t.Fatalf("config path = %q, want %q", runtime.Config.Path, path)
	}
	if err := WriteDefault(
		runtime.Config.Path,
		runtime.Paths.Home,
		runtime.Paths.Roots[pfmengine.Claude],
		false,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}

	t.Setenv(paths.EnvConfig, "")
	runtime, err = LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	if runtime.Config.Path != "" || runtime.Config.Exists {
		t.Fatalf("unresolved config = %+v", runtime.Config)
	}
	if err := WriteDefault(
		"",
		home,
		nil,
		false,
	); err == nil ||
		!strings.Contains(err.Error(), "no source repo recorded") {
		t.Fatalf("WriteDefault without a marker = %v", err)
	}
}

// TestRuntimeOrDefaultUsesTheCallersRuntimeAsGiven pins the optional-runtime
// rule: a caller's runtime is used as given, and only a nil one loads the
// default — whose broken config is still an error, never silent defaults.
func TestRuntimeOrDefaultUsesTheCallersRuntimeAsGiven(t *testing.T) {
	given := Runtime{Config: Config{Path: "/given/config.toml"}}
	got, err := RuntimeOrDefault(&given)
	if err != nil || got.Config.Path != "/given/config.toml" {
		t.Fatalf("RuntimeOrDefault(&given) = %+v, %v; want the given runtime", got.Config, err)
	}
	home := t.TempDir()
	t.Setenv(paths.EnvHome, home)
	path := filepath.Join(home, "pfm.config.json")
	t.Setenv(paths.EnvConfig, path)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("this is [not a config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if runtime, err := RuntimeOrDefault(nil); err == nil {
		t.Fatalf("RuntimeOrDefault(nil) ran on %+v past a broken default config", runtime.Config)
	}
}

func TestStateDatabasePrecedence(t *testing.T) {
	home := t.TempDir()
	t.Setenv(paths.EnvHome, home)
	t.Setenv(paths.EnvStateDB, "")
	t.Setenv(paths.EnvCacheDB, "")
	path := filepath.Join(t.TempDir(), "pfm.config.json")
	if err := os.WriteFile(
		path,
		[]byte(`{"version":2,"state":{"db":"~/x/a.db","cacheDb":"~/x/b.db"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, stateEnv, cacheEnv, stateWant, cacheWant string }{
		{"config", "", "", filepath.Join(home, "x", "a.db"), filepath.Join(home, "x", "b.db")},
		{"env", filepath.Join(home, "env", "state.db"), filepath.Join(home, "env", "cache.db"), filepath.Join(home, "env", "state.db"), filepath.Join(home, "env", "cache.db")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(paths.EnvStateDB, tc.stateEnv)
			t.Setenv(paths.EnvCacheDB, tc.cacheEnv)
			got, err := LoadRuntime(path)
			if err != nil {
				t.Fatal(err)
			}
			if got.Paths.StateDB != tc.stateWant || got.Paths.CacheDB != tc.cacheWant {
				t.Fatalf("paths = %q, %q", got.Paths.StateDB, got.Paths.CacheDB)
			}
			if got.Config.Source("state.db") != SourceFile || got.Config.Source("state.cacheDb") != SourceFile {
				t.Fatalf("sources = %v", got.Config.Sources)
			}
		})
	}
}

func TestStatePathsFromPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, config, stateEnv, cacheEnv, stateKey, cacheKey string
		missing, wantError                                   bool
	}{
		{name: "both env skip broken config", config: "not json", stateEnv: "env-state", cacheEnv: "env-cache", stateKey: "env-state", cacheKey: "env-cache"},
		{name: "one env and config", config: `{"version":2,"state":{"db":"~/key-state.db","cacheDb":"~/key-cache.db"}}`, stateEnv: "env-state", stateKey: "env-state", cacheKey: "key-cache.db"},
		{name: "config keys", config: `{"version":2,"state":{"db":"~/key-state.db","cacheDb":"~/key-cache.db"}}`, stateKey: "key-state.db", cacheKey: "key-cache.db"},
		{name: "no state object", config: `{"version":2}`},
		{name: "missing config file", missing: true},
		{name: "explicit missing config file", missing: true},
		{name: "broken config", config: "not json", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			configPath := filepath.Join(home, "pfm.config.json")
			if !tc.missing {
				if err := os.WriteFile(configPath, []byte(tc.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			env := &paths.MapEnv{Values: map[string]string{
				paths.EnvConfig: configPath, paths.EnvStateDB: tc.stateEnv, paths.EnvCacheDB: tc.cacheEnv,
			}}
			if tc.name == "missing config file" {
				delete(env.Values, paths.EnvConfig)
			}
			state, cache, err := StatePathsFrom(env, home)
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), configPath) {
					t.Fatalf("error = %v, want config path", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			wantState, wantCache := paths.DefaultStateDB(home), paths.DefaultCacheDB(home)
			if tc.stateKey != "" {
				wantState = tc.stateKey
				if tc.stateEnv == "" {
					wantState = filepath.Join(home, tc.stateKey)
				}
			}
			if tc.cacheKey != "" {
				wantCache = tc.cacheKey
				if tc.cacheEnv == "" {
					wantCache = filepath.Join(home, tc.cacheKey)
				}
			}
			if state != wantState || cache != wantCache {
				t.Fatalf("state, cache = %q, %q; want %q, %q", state, cache, wantState, wantCache)
			}
		})
	}
}

// legacyConfigHome builds a jailed home whose default load finds no clone
// config, with XDG_CONFIG_HOME pointed into it; withMarker records a clone
// that has no pfm.config.json.
func legacyConfigHome(t *testing.T, withMarker bool) (home, legacyDir, target string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv(paths.EnvHome, home)
	t.Setenv(paths.EnvConfig, "")
	xdg := filepath.Join(home, "xdg")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	legacyDir = filepath.Join(xdg, "pfm")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	target = "no clone config"
	if withMarker {
		clone := filepath.Join(home, "clone")
		if err := os.MkdirAll(clone, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
			t.Fatal(err)
		}
		target = filepath.Join(clone, FileName)
	}
	return home, legacyDir, target
}

func TestLoadRuntimeRefusesDefaultsWhileLegacyConfigWaits(t *testing.T) {
	for _, marker := range []bool{false, true} {
		for _, name := range []string{FileName, LegacyFileName} {
			t.Run(fmt.Sprintf("marker=%v/%s", marker, name), func(t *testing.T) {
				_, legacyDir, target := legacyConfigHome(t, marker)
				legacy := filepath.Join(legacyDir, name)
				if err := os.WriteFile(legacy, []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
				_, err := LoadRuntime("")
				if !errors.Is(err, ErrNotMigrated) {
					t.Fatalf("LoadRuntime(\"\") = %v, want ErrNotMigrated", err)
				}
				if !strings.HasPrefix(err.Error(), "config not migrated: run pfm install") {
					t.Fatalf("error %q does not begin with the remedy", err)
				}
				for _, want := range []string{legacy, target} {
					if !strings.Contains(err.Error(), want) {
						t.Fatalf("error %q lacks %q", err, want)
					}
				}
				runtime, err := LoadDiagnosticRuntime("")
				if err != nil {
					t.Fatalf("LoadDiagnosticRuntime(\"\") = %v", err)
				}
				if !errors.Is(runtime.ConfigError, ErrNotMigrated) || len(runtime.Config.Accounts) == 0 {
					t.Fatalf("diagnostic runtime = %+v, want defaults carrying ErrNotMigrated", runtime)
				}
				if _, err := LoadInstallRuntime(""); err != nil {
					t.Fatalf("LoadInstallRuntime(\"\") = %v, want the installer to load", err)
				}
			})
		}
	}
}

func TestLoadRuntimeNothingToMigrateRunsOnDefaults(t *testing.T) {
	legacyConfigHome(t, false)
	runtime, err := LoadRuntime("")
	if err != nil {
		t.Fatalf("LoadRuntime(\"\") = %v", err)
	}
	if runtime.Config.Exists || len(runtime.Config.Accounts) == 0 {
		t.Fatalf("Config = %+v, want defaults", runtime.Config)
	}
}

func TestLoadRuntimeExplicitConfigIgnoresLegacyFiles(t *testing.T) {
	home, legacyDir, _ := legacyConfigHome(t, false)
	if err := os.WriteFile(filepath.Join(legacyDir, FileName), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	named := filepath.Join(home, "named", FileName)
	if _, err := LoadRuntime(named); err != nil {
		t.Fatalf("LoadRuntime(--config) = %v", err)
	}
	t.Setenv(paths.EnvConfig, named)
	if _, err := LoadRuntime(""); err != nil {
		t.Fatalf("LoadRuntime under PFM_CONFIG = %v", err)
	}
}
