package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const DevelopmentVersion = "dev"

// Runtime is the resolved machine policy for one pfm process: the effective
// config and the filesystem locations it implies. It is loaded exactly once
// per invocation and then passed, immutable, to every package that consumes
// machine policy — the CLI, the fleet scan, the MCP server.
//
// ConfigError is set only by LoadDiagnosticRuntime: a diagnostic command runs
// on defaults over a broken config and must still report the original error.
//
// ConfigExplicit records whether the caller named a --config path other than
// the location ResolvePath would select without the flag. An explicit path
// that turns out not to exist (Config.Exists == false) is a caller error, not
// a fresh machine — callers that converge host wiring from Config must not
// treat that combination as "nothing configured yet".
type Runtime struct {
	Config         Config
	Paths          paths.Values
	ConfigError    error
	ConfigExplicit bool
	Version        string
}

func (runtime Runtime) IsRelease() bool {
	return runtime.Version != "" && runtime.Version != DevelopmentVersion
}

// DisplayVersion returns a stamped release version or an identifying VCS
// suffix for an unstamped development build.
func DisplayVersion(stamped string) string {
	if stamped != DevelopmentVersion {
		return stamped
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return stamped
	}
	return ResolveDevVersion(info.Settings)
}

// ResolveDevVersion formats the VCS settings embedded in a development build.
func ResolveDevVersion(settings []debug.BuildSetting) string {
	var revision string
	var modified bool
	for _, setting := range settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}
	if revision == "" {
		return DevelopmentVersion
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	if modified {
		return fmt.Sprintf("dev (%s, modified)", revision)
	}
	return fmt.Sprintf("dev (%s)", revision)
}

// OptionalRuntime returns the caller's first runtime, or loads the default.
func OptionalRuntime(runtimes []Runtime) (Runtime, error) {
	if len(runtimes) != 0 {
		return runtimes[0], nil
	}
	return LoadRuntime("")
}

// ErrNotMigrated marks a default config load refused because a legacy
// {LegacyConfigDir} config still waits for pfm install to move it into the clone.
var ErrNotMigrated = errors.New("config not migrated: run pfm install")

// LoadRuntime resolves paths, loads the config at configPath (the default
// location when empty), and keeps Claude's resolved transcript roots.
// A broken config is an error, and so is a default load that would run on
// defaults while a legacy config waits (ErrNotMigrated).
func LoadRuntime(configPath string) (Runtime, error) {
	runtime, err := LoadInstallRuntime(configPath)
	if err != nil {
		return Runtime{}, err
	}
	if err := checkLegacyConfig(configPath, runtime.Paths.Home, runtime.Config); err != nil {
		return Runtime{}, err
	}
	return runtime, nil
}

// LoadInstallRuntime is LoadRuntime without the ErrNotMigrated refusal: the
// installer is the command that migrates the legacy config.
func LoadInstallRuntime(configPath string) (Runtime, error) {
	resolved, err := paths.Resolve()
	if err != nil {
		return Runtime{}, fmt.Errorf("resolve paths: %w", err)
	}
	if err := RefuseAmbientConfigHome(resolved.Home); err != nil {
		return Runtime{}, err
	}
	effective, err := Load(
		configPath,
		resolved.Home,
		resolved.Roots[pfmengine.Claude],
		resolved.FirstRoot(pfmengine.Codex),
	)
	if err != nil {
		return Runtime{}, err
	}
	configExplicit, err := configPathIsExplicit(configPath, resolved.Home)
	if err != nil {
		return Runtime{}, err
	}
	applyStatePaths(&resolved, effective)
	resolved.Roots[pfmengine.Codex] = effective.CodexHomes()
	return Runtime{Config: effective, Paths: resolved, ConfigExplicit: configExplicit}, nil
}

func configPathIsExplicit(configPath, home string) (bool, error) {
	if configPath == "" {
		return false, nil
	}
	named, err := filepath.Abs(configPath)
	if err != nil {
		return false, fmt.Errorf("resolve --config path %q: %w", configPath, err)
	}
	// An older pfm update runs its candidate as `pfm --config <its default>
	// install --yes`; that default is the legacy config directory, which the
	// HostLayout reconciler migrates into the clone. Naming it is not explicit.
	if filepath.Dir(named) == LegacyConfigDir(paths.OSEnv{}, home) {
		if base := filepath.Base(named); base == FileName || base == LegacyFileName {
			return false, nil
		}
	}
	defaultPath, resolveErr := ResolvePath(home)
	if resolveErr != nil {
		// No default location resolves (no clone marker, or a malformed
		// PFM_CONFIG), so the named path cannot be it; the resolution error
		// surfaces wherever the default path is loaded.
		return true, nil
	}
	defaultAbsolute, err := filepath.Abs(defaultPath)
	if err != nil {
		return false, fmt.Errorf("resolve default config path %q: %w", defaultPath, err)
	}
	return named != defaultAbsolute, nil
}

// checkLegacyConfig refuses a default load that found no clone config while
// a legacy pfm.config.json or config.json waits in LegacyConfigDir. A named
// --config path or PFM_CONFIG is never refused.
func checkLegacyConfig(configPath, home string, loaded Config) error {
	env := paths.OSEnv{}
	if configPath != "" || env.Get(paths.EnvConfig) != "" || loaded.Exists {
		return nil
	}
	target, resolveErr := ResolvePath(home)
	if resolveErr != nil {
		target = "no clone config"
	}
	dir := LegacyConfigDir(env, home)
	for _, name := range []string{FileName, LegacyFileName} {
		legacy := filepath.Join(dir, name)
		_, err := os.Lstat(legacy)
		if err == nil {
			return fmt.Errorf("%w (%s present, %s absent)", ErrNotMigrated, legacy, target)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect legacy config %s: %w", legacy, err)
		}
	}
	return nil
}

// LegacyConfigDir is the config directory pfm resolved before the config
// moved into the clone: $XDG_CONFIG_HOME/pfm when that is absolute, else
// {home}/.config/pfm.
func LegacyConfigDir(env paths.Env, home string) string {
	if xdg := env.Get("XDG_CONFIG_HOME"); filepath.IsAbs(xdg) {
		return filepath.Join(xdg, "pfm")
	}
	return filepath.Join(home, ".config", "pfm")
}

// RuntimeOrDefault is the caller's runtime, or the default one loaded now —
// the rule for every package API whose runtime parameter is optional.
func RuntimeOrDefault(runtime *Runtime) (Runtime, error) {
	if runtime != nil {
		return *runtime, nil
	}
	return LoadRuntime("")
}

// LoadDiagnosticRuntime is LoadRuntime for commands that must stay usable on
// a broken config: they run on defaults and carry the load error in
// ConfigError to the visible command surface.
func LoadDiagnosticRuntime(configPath string) (Runtime, error) {
	resolved, err := paths.Resolve()
	if err != nil {
		return Runtime{}, fmt.Errorf("resolve paths: %w", err)
	}
	if err := RefuseAmbientConfigHome(resolved.Home); err != nil {
		return Runtime{}, err
	}
	effective, configErr := Load(
		configPath,
		resolved.Home,
		resolved.Roots[pfmengine.Claude],
		resolved.FirstRoot(pfmengine.Codex),
	)
	configExplicit, err := configPathIsExplicit(configPath, resolved.Home)
	if err != nil {
		return Runtime{}, err
	}
	if configErr == nil {
		configErr = checkLegacyConfig(configPath, resolved.Home, effective)
	}
	if configErr == nil {
		applyStatePaths(&resolved, effective)
		resolved.Roots[pfmengine.Codex] = effective.CodexHomes()
		return Runtime{Config: effective, Paths: resolved, ConfigExplicit: configExplicit}, nil
	}
	path := configPath
	if path == "" {
		path, _ = ResolvePath(resolved.Home)
	}
	effective = Defaults(resolved.Home, resolved.Roots[pfmengine.Claude], resolved.FirstRoot(pfmengine.Codex))
	effective.Path = path
	effective.Exists = true
	resolved.Roots[pfmengine.Codex] = effective.CodexHomes()
	return Runtime{Config: effective, Paths: resolved, ConfigError: configErr, ConfigExplicit: configExplicit}, nil
}

func applyStatePaths(resolved *paths.Values, config Config) {
	if (paths.OSEnv{}).Get(paths.EnvStateDB) == "" {
		resolved.StateDB = config.State.DB
	}
	if (paths.OSEnv{}).Get(paths.EnvCacheDB) == "" {
		resolved.CacheDB = config.State.CacheDB
	}
}
