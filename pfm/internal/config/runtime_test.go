package config

import (
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
	t.Setenv("XDG_CONFIG_HOME", "")
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

// TestLoadRuntimePointsTheEngineRootsAtTheRoster pins the reconcile step: the
// resolved roots are the configured accounts', not the host defaults.
func TestLoadRuntimePointsTheEngineRootsAtTheRoster(t *testing.T) {
	t.Setenv(paths.EnvHome, t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
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

// TestLoadRuntimeRecordsWhetherConfigWasExplicit pins issue #24's missing
// non-default config guard while treating a named spelling of the resolved
// default path the same as an omitted flag.
func TestLoadRuntimeRecordsWhetherConfigWasExplicit(t *testing.T) {
	home := t.TempDir()
	t.Setenv(paths.EnvHome, home)
	t.Setenv("XDG_CONFIG_HOME", "")
	implicit, err := LoadRuntime("")
	if err != nil {
		t.Fatalf("LoadRuntime(\"\") = %v", err)
	}
	if implicit.ConfigExplicit {
		t.Fatalf("LoadRuntime(\"\").ConfigExplicit = true, want false for the default location")
	}
	defaultPath := ResolvePath(home)
	namedDefault := filepath.Dir(defaultPath) + "/../pfm/" + filepath.Base(defaultPath)
	named, err := LoadRuntime(namedDefault)
	if err != nil {
		t.Fatalf("LoadRuntime(%q) = %v", namedDefault, err)
	}
	if named.ConfigExplicit {
		t.Fatalf("LoadRuntime(%q).ConfigExplicit = true, want false for the default location", namedDefault)
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

// TestConfigInitWritesInsideTheJailAndRefusesAnAmbientHome pins the write
// half of L3-F9: `pfm config init` (cmd/pfm/config_command.go runConfigInit)
// resolves its target through LoadRuntime before WriteDefault ever touches
// disk, so a jailed PFM_HOME with a properly re-homed XDG_CONFIG_HOME writes
// inside the jail, and the SAME jailed PFM_HOME with an ambient
// XDG_CONFIG_HOME left pointing outside it is refused before WriteDefault
// ever runs — never a silent write into the operator's real pfm/config.*.
func TestConfigInitWritesInsideTheJailAndRefusesAnAmbientHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv(paths.EnvHome, home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	runtime, err := LoadRuntime("")
	if err != nil {
		t.Fatalf("LoadRuntime() over a properly jailed home = %v", err)
	}
	if err := WriteDefault(
		runtime.Config.Path, runtime.Paths.Home, runtime.Paths.Roots[pfmengine.Claude], false,
	); err != nil {
		t.Fatalf("WriteDefault() = %v", err)
	}
	if !strings.HasPrefix(runtime.Config.Path, home) {
		t.Fatalf("config init wrote %q, want it inside the jailed home %q", runtime.Config.Path, home)
	}
	if _, statErr := os.Stat(runtime.Config.Path); statErr != nil {
		t.Fatalf("stat written config %q: %v", runtime.Config.Path, statErr)
	}

	// The same jailed PFM_HOME, but XDG_CONFIG_HOME now points somewhere
	// that does not derive from it — the ambient leak L3-F9 names.
	ambient := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", ambient)
	if _, err := LoadRuntime(""); err == nil {
		t.Fatal("LoadRuntime() accepted an ambient XDG_CONFIG_HOME outside the jailed home")
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
	t.Setenv("XDG_CONFIG_HOME", "")
	path := ResolvePath(home)
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
