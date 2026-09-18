package config

import (
	"fmt"
	"runtime/debug"

	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/paths"
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
// ConfigExplicit records whether the caller named a --config path rather
// than letting resolveExistingPath pick the default one. An explicit path
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

// LoadRuntime resolves paths, loads the config at configPath (the default
// location when empty), and re-points the engine roots at the configured
// accounts. A broken config is an error.
func LoadRuntime(configPath string) (Runtime, error) {
	resolved, err := paths.Resolve()
	if err != nil {
		return Runtime{}, fmt.Errorf("resolve paths: %w", err)
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
	resolved.Roots[pfmengine.Claude] = effective.ProjectRoots()
	resolved.Roots[pfmengine.Codex] = effective.CodexHomes()
	return Runtime{Config: effective, Paths: resolved, ConfigExplicit: configPath != ""}, nil
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
	effective, configErr := Load(
		configPath,
		resolved.Home,
		resolved.Roots[pfmengine.Claude],
		resolved.FirstRoot(pfmengine.Codex),
	)
	if configErr == nil {
		resolved.Roots[pfmengine.Claude] = effective.ProjectRoots()
		resolved.Roots[pfmengine.Codex] = effective.CodexHomes()
		return Runtime{Config: effective, Paths: resolved, ConfigExplicit: configPath != ""}, nil
	}
	path := configPath
	if path == "" {
		path = ResolvePath(resolved.Home)
	}
	effective = Defaults(resolved.Home, resolved.Roots[pfmengine.Claude], resolved.FirstRoot(pfmengine.Codex))
	effective.Path = path
	effective.Exists = true
	resolved.Roots[pfmengine.Claude] = effective.ProjectRoots()
	resolved.Roots[pfmengine.Codex] = effective.CodexHomes()
	return Runtime{Config: effective, Paths: resolved, ConfigError: configErr, ConfigExplicit: configPath != ""}, nil
}
