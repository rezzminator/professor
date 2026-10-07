package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
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
		{name: "unrelated invalid key keeps state", config: `{"version":2,"accounts":[],"ask":{"engine":"cc"},"state":{"db":"~/key-state.db"}}`, stateKey: "key-state.db"},
		{name: "unsupported version", config: `{"version":99,"state":{"db":"~/key-state.db"}}`, wantError: true},
		{name: "empty state key", config: `{"version":2,"state":{"db":" "}}`, wantError: true},
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
				if !strings.HasPrefix(err.Error(), "config not migrated: run pfm doctor for the fix") {
					t.Fatalf("error %q does not begin with the remedy", err)
				}
				want := fmt.Sprintf(
					"config not migrated: run pfm doctor for the fix (%s present, %s absent)",
					legacy,
					target,
				)
				if err.Error() != want {
					t.Fatalf("error=%q, want %q", err, want)
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

func TestLegacyConfigWaiting(t *testing.T) {
	for _, name := range []string{FileName, LegacyFileName} {
		for _, targetState := range []string{"present", "absent", "empty"} {
			t.Run(name+"/target="+targetState, func(t *testing.T) {
				root := t.TempDir()
				legacyDir := filepath.Join(root, "legacy")
				if err := os.MkdirAll(legacyDir, 0o700); err != nil {
					t.Fatal(err)
				}
				legacy := filepath.Join(legacyDir, name)
				if err := os.WriteFile(legacy, []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(root, "clone", FileName)
				switch targetState {
				case "present":
					if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
						t.Fatal(err)
					}
				case "empty":
					target = ""
				}
				got, err := LegacyConfigWaiting(legacyDir, target)
				if err != nil {
					t.Fatalf("LegacyConfigWaiting = %v", err)
				}
				want := legacy
				if targetState == "present" {
					want = ""
				}
				if got != want {
					t.Fatalf("LegacyConfigWaiting = %q, want %q", got, want)
				}
			})
		}
	}
	t.Run("no legacy file", func(t *testing.T) {
		got, err := LegacyConfigWaiting(t.TempDir(), "")
		if err != nil || got != "" {
			t.Fatalf("LegacyConfigWaiting = %q, %v; want empty, nil", got, err)
		}
	})
	t.Run("lstat error", func(t *testing.T) {
		notDir := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(notDir, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := LegacyConfigWaiting(notDir, "")
		if err == nil || got != "" {
			t.Fatalf("LegacyConfigWaiting under a file = %q, %v; want an error", got, err)
		}
		if !strings.Contains(err.Error(), "inspect legacy config "+filepath.Join(notDir, FileName)) {
			t.Fatalf("error %q does not name the legacy path", err)
		}
	})
}

func TestLoadInstallRuntimeMissingNamedLegacyConfigIsExplicit(t *testing.T) {
	_, legacyDir, _ := legacyConfigHome(t, true)
	for _, name := range []string{FileName, LegacyFileName} {
		t.Run(name, func(t *testing.T) {
			runtime, err := LoadInstallRuntime(filepath.Join(legacyDir, name))
			if err != nil {
				t.Fatal(err)
			}
			if !runtime.ConfigExplicit || runtime.Config.Exists {
				t.Fatalf(
					"explicit=%v exists=%v, want explicit missing config",
					runtime.ConfigExplicit,
					runtime.Config.Exists,
				)
			}
		})
	}
}

func TestLoadRuntimeUnusableMarker(t *testing.T) {
	for _, tc := range []struct {
		name               string
		load               func(string) (Runtime, error)
		refuse, diagnostic bool
	}{
		{name: "ordinary", load: LoadRuntime, refuse: true},
		{name: "install", load: LoadInstallRuntime},
		{name: "diagnostic", load: LoadDiagnosticRuntime, diagnostic: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv(paths.EnvHome, home)
			t.Setenv(paths.EnvConfig, "")
			missing := filepath.Join(home, "missing-clone")
			marker := paths.SourceRepoPath(home)
			if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(marker, []byte(missing+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := tc.load("")
			if tc.refuse {
				if !errors.Is(err, paths.ErrSourceRepoUnusable) || !strings.Contains(err.Error(), missing) ||
					!strings.Contains(err.Error(), "run pfm install from the clone") {
					t.Fatalf("ordinary marker load = %v, want the recorded clone and remedy", err)
				}
				return
			}
			if err != nil || got.Config.Path != "" || got.Config.Exists {
				t.Fatalf("runtime marker load: path=%q exists=%v err=%v", got.Config.Path, got.Config.Exists, err)
			}
			if tc.diagnostic && (!errors.Is(got.ConfigError, paths.ErrSourceRepoUnusable) ||
				!strings.Contains(got.ConfigError.Error(), missing)) {
				t.Fatalf("diagnostic marker error = %v, want the recorded clone", got.ConfigError)
			}
		})
	}
}

func TestDiagnosticStatePaths(t *testing.T) {
	for _, tc := range []struct {
		name, content, state string
	}{
		{"unrelated key", `{"version":2,"accounts":[],"ask":{"engine":"cc"},"state":{"db":"~/diag.db"}}`, "diag.db"},
		{"broken JSON", `{"version":2`, ""},
		{"invalid state", `{"version":2,"state":{"db":false}}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv(paths.EnvHome, home)
			t.Setenv(paths.EnvStateDB, "")
			t.Setenv(paths.EnvCacheDB, "")
			resolved, err := paths.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(home, FileName)
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := LoadDiagnosticRuntime(path)
			want := resolved.StateDB
			if tc.state != "" {
				want = filepath.Join(home, tc.state)
			}
			if err != nil || got.ConfigError == nil || !got.Config.Exists || got.Paths.StateDB != want ||
				got.Paths.CacheDB != resolved.CacheDB || !strings.Contains(got.ConfigError.Error(), path) {
				t.Fatalf("diagnostic paths=%q,%q exists=%v load=%v config=%v, want state=%q",
					got.Paths.StateDB, got.Paths.CacheDB, got.Config.Exists, err, got.ConfigError, want)
			}
			defer UseConfigPath(got.Config.Path, got.Config.State)()
			state, cache, readErr := StatePathsFrom(paths.OSEnv{}, home)
			if readErr != nil || state != got.Paths.StateDB || cache != got.Paths.CacheDB {
				t.Fatalf(
					"diagnostic store paths=%q,%q error=%v; runtime=%q,%q",
					state,
					cache,
					readErr,
					got.Paths.StateDB,
					got.Paths.CacheDB,
				)
			}
			for _, overrides := range []map[string]string{
				{paths.EnvStateDB: filepath.Join(home, "env-state.db")},
				{paths.EnvCacheDB: filepath.Join(home, "env-cache.db")},
				{paths.EnvStateDB: filepath.Join(home, "env-state.db"), paths.EnvCacheDB: filepath.Join(home, "env-cache.db")},
			} {
				env := &paths.MapEnv{Values: overrides}
				state, cache, err := StatePathsFrom(env, home)
				wantState, wantCache := got.Paths.StateDB, got.Paths.CacheDB
				if overrides[paths.EnvStateDB] != "" {
					wantState = overrides[paths.EnvStateDB]
				}
				if overrides[paths.EnvCacheDB] != "" {
					wantCache = overrides[paths.EnvCacheDB]
				}
				if err != nil || state != wantState || cache != wantCache {
					t.Fatalf("snapshot overrides=%v paths=%q,%q error=%v", overrides, state, cache, err)
				}
			}
			if tc.name == "broken JSON" && !strings.Contains(got.ConfigError.Error(), "JSON") {
				t.Fatalf("broken JSON config error = %v, want parse error", got.ConfigError)
			}
		})
	}
}

func TestStatePathsFromMarkerResolution(t *testing.T) {
	for _, tc := range []struct {
		name, override       string
		missingClone, pinned bool
	}{
		{"vanished clone", "", true, false},
		{"whitespace override", "   ", false, false},
		{"vanished clone, process pinned to no config", "", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			missing := filepath.Join(home, "missing-clone")
			if tc.missingClone {
				marker := paths.SourceRepoPath(home)
				if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(marker, []byte(missing+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			env := &paths.MapEnv{Values: map[string]string{paths.EnvConfig: tc.override}}
			if tc.pinned {
				defer UseConfigPath("")()
			}
			state, cache, err := StatePathsFrom(env, home)
			if tc.missingClone && !tc.pinned {
				if state != "" || cache != "" || !errors.Is(err, paths.ErrSourceRepoUnusable) ||
					!strings.Contains(err.Error(), missing) {
					t.Fatalf("marker state paths=%q,%q err=%v, want unusable clone", state, cache, err)
				}
				return
			}
			if err != nil || state != paths.DefaultStateDB(home) || cache != paths.DefaultCacheDB(home) {
				t.Fatalf("%s: state paths=%q,%q err=%v, want defaults", tc.name, state, cache, err)
			}
		})
	}
}

func TestStatePathsFromPinnedConfig(t *testing.T) {
	home := t.TempDir()
	pinned, fromEnv := filepath.Join(home, "pinned.json"), filepath.Join(home, "env.json")
	for path, content := range map[string]string{
		pinned:  `{"version":2,"state":{"db":"~/pinned.db"}}`,
		fromEnv: `{"version":2,"state":{"db":"~/env.db"}}`,
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, stateEnv := range []string{"", "/env/state.db"} {
		t.Run("state="+stateEnv, func(t *testing.T) {
			env := &paths.MapEnv{Values: map[string]string{paths.EnvConfig: fromEnv, paths.EnvStateDB: stateEnv}}
			restore := UseConfigPath(pinned)
			defer restore()
			state, cache, err := StatePathsFrom(env, home)
			want := filepath.Join(home, "pinned.db")
			if stateEnv != "" {
				want = stateEnv
			}
			if err != nil || state != want || cache != paths.DefaultCacheDB(home) {
				t.Fatalf("pinned paths=%q,%q err=%v, want %q", state, cache, err, want)
			}
			restoreNested := UseConfigPath(fromEnv)
			restoreNested()
			var wg sync.WaitGroup
			for range 16 {
				wg.Go(func() {
					restoreSame := UseConfigPath(pinned)
					defer restoreSame()
					got, _, readErr := StatePathsFrom(env, home)
					if readErr != nil || got != want {
						t.Errorf("concurrent pinned state=%q err=%v, want %q", got, readErr, want)
					}
				})
			}
			wg.Wait()
			restore()
			state, _, err = StatePathsFrom(env, home)
			want = filepath.Join(home, "env.db")
			if stateEnv != "" {
				want = stateEnv
			}
			if err != nil || state != want {
				t.Fatalf("restored state=%q err=%v, want %q", state, err, want)
			}
		})
	}
}

func TestLoadRuntimeWhitespaceOverrideStillRefusesLegacy(t *testing.T) {
	_, legacyDir, _ := legacyConfigHome(t, false)
	if err := os.WriteFile(filepath.Join(legacyDir, FileName), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvConfig, "  ")
	if _, err := LoadRuntime(""); !errors.Is(err, ErrNotMigrated) {
		t.Fatalf("blank override load=%v, want ErrNotMigrated", err)
	}
}
