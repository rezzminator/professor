package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const DevelopmentVersion = "dev"

var configPathPin struct {
	sync.RWMutex
	path   string
	pinned bool
	state  *State
}

// UseConfigPath pins the loaded config for database readers until restore runs.
// An optional resolved state pins the runtime snapshot, including diagnostic
// defaults over a broken config, without re-reading the config file.
// An empty path pins the defaults: a runtime loaded with no config file (an
// install over an unusable marker) reads the same database pair it reports.
func UseConfigPath(path string, states ...State) (restore func()) {
	configPathPin.Lock()
	previous, previouslyPinned, previousState := configPathPin.path, configPathPin.pinned, configPathPin.state
	configPathPin.path, configPathPin.pinned, configPathPin.state = path, true, nil
	if len(states) != 0 {
		state := states[0]
		configPathPin.state = &state
	}
	configPathPin.Unlock()
	return func() {
		configPathPin.Lock()
		configPathPin.path, configPathPin.pinned, configPathPin.state = previous, previouslyPinned, previousState
		configPathPin.Unlock()
	}
}

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

// DepsOptions is the dependency registry's options for this runtime: its home
// and the configured launch binary of every engine, so each engine row probes
// the binary pfm launches.
func (runtime Runtime) DepsOptions() deps.Options {
	return deps.Options{
		Home:           runtime.Paths.Home,
		ClaudeBinary:   runtime.Config.Claude.Binary,
		CodexBinary:    runtime.Config.Codex.Binary,
		OpenCodeBinary: runtime.Config.OpenCode.Binary,
	}
}

// WithConfig applies a newly loaded install seed using the runtime's database
// precedence, preserving independently overridden state and cache paths.
func (runtime Runtime) WithConfig(config Config, env paths.Env) Runtime {
	runtime.Config = config
	applyStatePaths(&runtime.Paths, config, env)
	return runtime
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

// LoadRuntime resolves paths, loads the config at configPath (the default
// location when empty), and keeps Claude's resolved transcript roots.
// A broken config is an error.
func LoadRuntime(configPath string) (Runtime, error) {
	return loadConfigRuntime(configPath, false)
}

// LoadInstallRuntime permits defaults over an unusable marker: install names
// the marker fallback.
func LoadInstallRuntime(configPath string) (Runtime, error) {
	return loadConfigRuntime(configPath, true)
}

func loadConfigRuntime(configPath string, install bool) (Runtime, error) {
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
		var markerErr *sourceRepoMarkerError
		if !install || !errors.As(err, &markerErr) {
			return Runtime{}, err
		}
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
	effective.Exists = path != ""
	if state, err := loadState(path, resolved.Home); err == nil {
		effective.State = state
		applyStatePaths(&resolved, effective, paths.OSEnv{})
	}
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
	configPathPin.RLock()
	configPath, pinned, pinnedState := configPathPin.path, configPathPin.pinned, configPathPin.state
	configPathPin.RUnlock()
	if !pinned {
		configPath, err = ResolvePathFrom(env, home)
		if err != nil {
			if configOverridden(env) {
				return "", "", err
			}
			if markerErr := configMarkerError(err); markerErr != nil {
				return "", "", markerErr
			}
		}
	}
	var state State
	if pinnedState != nil {
		state = *pinnedState
	} else {
		state, err = loadState(configPath, home)
		if err != nil {
			return "", "", err
		}
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
	if raw.Version != nil && *raw.Version != 1 && *raw.Version != Version {
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
