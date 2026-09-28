package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

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
	applyStatePaths(&resolved, effective, paths.OSEnv{})
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
	legacy, err := LegacyConfigWaiting(LegacyConfigDir(env, home), "")
	if err != nil {
		return err
	}
	if legacy != "" {
		return fmt.Errorf("%w (%s present, %s absent)", ErrNotMigrated, legacy, target)
	}
	return nil
}

// LegacyConfigWaiting returns the legacy pfm.config.json or config.json in
// legacyDir when one exists while target does not ("" counts as absent), else
// "". It ignores PFM_CONFIG and --config: the fleet units run a default load,
// so a rollback asks it whether the restored layout is one pfm refuses.
func LegacyConfigWaiting(legacyDir, target string) (string, error) {
	if target != "" {
		_, err := os.Lstat(target)
		if err == nil {
			return "", nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("inspect target config %s: %w", target, err)
		}
	}
	for _, name := range []string{FileName, LegacyFileName} {
		legacy := filepath.Join(legacyDir, name)
		_, err := os.Lstat(legacy)
		if err == nil {
			return legacy, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("inspect legacy config %s: %w", legacy, err)
		}
	}
	return "", nil
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
		applyStatePaths(&resolved, effective, paths.OSEnv{})
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

func applyStatePaths(resolved *paths.Values, config Config, env paths.Env) {
	if env.Get(paths.EnvStateDB) == "" {
		resolved.StateDB = config.State.DB
	}
	if env.Get(paths.EnvCacheDB) == "" {
		resolved.CacheDB = config.State.CacheDB
	}
}

// StatePathsFrom applies env, config, then default precedence independently
// to the two database paths. Explicit env paths need no config read.
func StatePathsFrom(env paths.Env, home string) (stateDB, cacheDB string, err error) {
	stateDB, cacheDB = env.Get(paths.EnvStateDB), env.Get(paths.EnvCacheDB)
	if stateDB != "" && cacheDB != "" {
		return stateDB, cacheDB, nil
	}
	if err := RefuseAmbientConfigHomeFrom(env, home); err != nil {
		return "", "", err
	}
	configPath, err := ResolvePathFrom(env, home)
	if err != nil {
		if env.Get(paths.EnvConfig) != "" {
			return "", "", err
		}
		configPath = ""
	}
	state, err := loadState(configPath, home)
	if err != nil {
		return "", "", err
	}
	resolved := paths.Values{StateDB: stateDB, CacheDB: cacheDB}
	applyStatePaths(&resolved, Config{State: state}, env)
	return resolved.StateDB, resolved.CacheDB, nil
}

// ResolvePaths resolves all process paths, including configured databases.
func ResolvePaths() (paths.Values, error) {
	resolved, err := paths.Resolve()
	if err != nil {
		return paths.Values{}, fmt.Errorf("resolve state paths: %w", err)
	}
	resolved.StateDB, resolved.CacheDB, err = StatePathsFrom(paths.OSEnv{}, resolved.Home)
	if err != nil {
		return paths.Values{}, fmt.Errorf("resolve state paths: %w", err)
	}
	return resolved, nil
}

// decodeVersioned strictly decodes a config file and checks its version, the
// validation every reader of the file shares.
func decodeVersioned(path string, content []byte) (rawConfig, error) {
	var raw rawConfig
	if err := decodeStrict(content, &raw); err != nil {
		return rawConfig{}, configJSONError(path, err, int64(len(content)))
	}
	if raw.Version == nil {
		return rawConfig{}, fmt.Errorf("config %s: required key %q is missing", path, keyVersion)
	}
	if *raw.Version != 1 && *raw.Version != Version {
		return rawConfig{}, fmt.Errorf("config %s: version must be 1 or %d, got %d", path, Version, *raw.Version)
	}
	return raw, nil
}

// applyStateKeys applies the file's state.db and state.cacheDb over the
// defaults already in result.
func applyStateKeys(result *Config, state *rawState, home string) error {
	if state == nil {
		return nil
	}
	for _, entry := range []struct {
		key    string
		raw    *string
		target *string
	}{
		{keyStateDB, state.DB, &result.State.DB},
		{keyStateCacheDB, state.CacheDB, &result.State.CacheDB},
	} {
		if entry.raw == nil {
			continue
		}
		if strings.TrimSpace(*entry.raw) == "" {
			return fmt.Errorf("config %s: %s must be non-empty", result.Path, entry.key)
		}
		value, err := expandHomePath(*entry.raw, home)
		if err != nil {
			return fmt.Errorf("config %s: %s: %w", result.Path, entry.key, err)
		}
		*entry.target = value
		result.Sources[entry.key] = SourceFile
	}
	return nil
}

// loadState reads only what locating the databases needs: the file's syntax,
// its version and its state keys. A setting elsewhere in the file that Load
// would refuse (an ask engine with no account, say) does not hide where the
// databases live; the command that uses that setting reports it.
func loadState(path, home string) (State, error) {
	result := Config{
		Path:    path,
		State:   State{DB: paths.DefaultStateDB(home), CacheDB: paths.DefaultCacheDB(home)},
		Sources: map[string]Source{},
	}
	if path == "" {
		return result.State, nil
	}
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return result.State, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("read config %s: %w", path, err)
	}
	raw, err := decodeVersioned(path, content)
	if err != nil {
		return State{}, err
	}
	if err := applyStateKeys(&result, raw.State, home); err != nil {
		return State{}, err
	}
	return result.State, nil
}
