package config

import (
	"os"
	"path/filepath"
	"testing"

	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/paths"
)

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

// TestLoadRuntimePointsTheEngineRootsAtTheRoster pins the reconcile step: the
// resolved roots are the configured accounts', not the host defaults.
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

// TestLoadRuntimeRecordsWhetherConfigWasExplicit is a REGRESSION test for
// issue #24 finding 4: an explicit --config path resolved before an update's
// candidate install is indistinguishable from the default location unless
// the runtime remembers the caller asked for it by name. Unfixed, Runtime
// carries no ConfigExplicit field at all — this test does not compile
// against the unfixed code, and that compile failure IS the watched-failing
// run.
func TestLoadRuntimeRecordsWhetherConfigWasExplicit(t *testing.T) {
	t.Setenv(paths.EnvHome, t.TempDir())
	implicit, err := LoadRuntime("")
	if err != nil {
		t.Fatalf("LoadRuntime(\"\") = %v", err)
	}
	if implicit.ConfigExplicit {
		t.Fatalf("LoadRuntime(\"\").ConfigExplicit = true, want false for the default location")
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
