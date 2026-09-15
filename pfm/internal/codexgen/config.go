package codexgen

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	configVersion = 1
	configRelPath = ".claude/codex-build.json"
)

// Config is the project-local policy for the Codex compiler.  It deliberately
// contains compiler behaviour only; machine settings (for example the Codex
// executable path) belong to pfm's machine configuration and are not read
// here.
type Config struct {
	Version         int               `json:"version"`
	GlobalCommands  bool              `json:"globalCommands"`
	ModelMap        map[string]string `json:"modelMap"`
	RootAdapter     string            `json:"rootAdapter"`
	AgentPreamble   string            `json:"agentPreamble"`
	ExcludeDirs     []string          `json:"excludeDirs"`
	Projects        []string          `json:"projects"`
	ExcludeProjects []string          `json:"excludeProjects"`
	NeverRegister   []string          `json:"neverRegister"`
	SuffixMode      string            `json:"suffixMode"`
	SuffixPrefix    string            `json:"suffixPrefix"`
}

// CLIOverrides contains values supplied by a codex command.  A non-nil slice
// or map is an explicit command-line value (including an intentionally empty
// value).  The Set* fields let callers explicitly clear scalar values, which
// cannot otherwise be distinguished from an omitted flag.
type CLIOverrides struct {
	ModelMap        map[string]string
	RootAdapter     string
	AgentPreamble   string
	ExcludeDirs     []string
	Projects        []string
	ExcludeProjects []string
	NeverRegister   []string
	SuffixMode      string
	SuffixPrefix    string

	SetRootAdapter   bool
	SetAgentPreamble bool
}

// defaultConfig is intentionally free of project-specific paths.  A caller
// that needs a different model roster, adapter, or project discovery policy
// supplies it through .claude/codex-build.json or CLIOverrides.
func defaultConfig() Config {
	return Config{
		Version:        configVersion,
		GlobalCommands: true,
		ModelMap: map[string]string{
			"opus":   "gpt-5.6-sol",
			"sonnet": "gpt-5.6-luna",
			"haiku":  "gpt-5.6-luna",
			"fable":  "gpt-5.6-sol",
		},
		RootAdapter:   defaultRootAdapter,
		AgentPreamble: defaultAgentPreamble,
		SuffixMode:    "project",
	}
}

// DefaultConfig exposes a copy of the compiler defaults to the command layer.
// Maps and slices are copied so a command cannot mutate the process-wide
// defaults by accident.
func DefaultConfig() Config { return cloneConfig(defaultConfig()) }

// LoadConfig loads the per-repository config and applies command-line values
// at the highest precedence.  A missing repository config is the normal
// defaults case; malformed, unknown, or version-incompatible JSON is an
// explicit error.
func LoadConfig(root string, cli CLIOverrides) (Config, error) {
	return loadConfig(root, cli)
}

func loadConfig(root string, cli CLIOverrides) (Config, error) {
	cfg := defaultConfig()
	path := filepath.Join(root, filepath.FromSlash(configRelPath))
	b, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return Config{}, fmt.Errorf("read Codex compiler config %s: %w", path, err)
		}
	} else {
		file, err := decodeConfigFile(b, path)
		if err != nil {
			return Config{}, err
		}
		if file.Version == nil {
			return Config{}, fmt.Errorf("config for Codex compiler %s: version is required", path)
		}
		if *file.Version != configVersion {
			return Config{}, fmt.Errorf(
				"config for Codex compiler %s: unsupported version %d (want %d)",
				path,
				*file.Version,
				configVersion,
			)
		}
		if file.GlobalCommands != nil {
			cfg.GlobalCommands = *file.GlobalCommands
		}
		if file.ModelMap != nil {
			for alias, model := range file.ModelMap {
				cfg.ModelMap[alias] = model
			}
		}
		if file.RootAdapter != nil {
			cfg.RootAdapter = *file.RootAdapter
		}
		if file.AgentPreamble != nil {
			cfg.AgentPreamble = *file.AgentPreamble
		}
		if file.ExcludeDirs != nil {
			cfg.ExcludeDirs = cloneStrings(file.ExcludeDirs)
		}
		if file.Projects != nil {
			cfg.Projects = cloneStrings(file.Projects)
		}
		if file.ExcludeProjects != nil {
			cfg.ExcludeProjects = cloneStrings(file.ExcludeProjects)
		}
		if file.NeverRegister != nil {
			cfg.NeverRegister = cloneStrings(file.NeverRegister)
		}
		if file.SuffixMode != nil {
			cfg.SuffixMode = *file.SuffixMode
		}
		if file.SuffixPrefix != nil {
			cfg.SuffixPrefix = *file.SuffixPrefix
		}
	}

	applyCLIOverrides(&cfg, cli)
	if err := validateConfig(cfg); err != nil {
		return Config{}, fmt.Errorf("config for Codex compiler: %w", err)
	}
	return cfg, nil
}

// configFile uses pointers for optional values.  This preserves the crucial
// distinction between a key omitted from JSON (inherit the default) and a key
// explicitly set to an empty string/list/map (clear the inherited value).
type configFile struct {
	Version         *int              `json:"version"`
	GlobalCommands  *bool             `json:"globalCommands"`
	ModelMap        map[string]string `json:"modelMap"`
	RootAdapter     *string           `json:"rootAdapter"`
	AgentPreamble   *string           `json:"agentPreamble"`
	ExcludeDirs     []string          `json:"excludeDirs"`
	Projects        []string          `json:"projects"`
	ExcludeProjects []string          `json:"excludeProjects"`
	NeverRegister   []string          `json:"neverRegister"`
	SuffixMode      *string           `json:"suffixMode"`
	SuffixPrefix    *string           `json:"suffixPrefix"`
}

func decodeConfigFile(data []byte, path string) (configFile, error) {
	var file configFile
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		return configFile{}, fmt.Errorf("decode Codex compiler config %s: %w", path, err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return configFile{}, fmt.Errorf("decode Codex compiler config %s: multiple JSON values", path)
		}
		return configFile{}, fmt.Errorf("decode Codex compiler config %s: trailing data: %w", path, err)
	}
	return file, nil
}

func applyCLIOverrides(cfg *Config, cli CLIOverrides) {
	if cli.ModelMap != nil {
		if cfg.ModelMap == nil {
			cfg.ModelMap = make(map[string]string)
		}
		for key, value := range cli.ModelMap {
			cfg.ModelMap[key] = value
		}
	}
	if cli.RootAdapter != "" || cli.SetRootAdapter {
		cfg.RootAdapter = cli.RootAdapter
	}
	if cli.AgentPreamble != "" || cli.SetAgentPreamble {
		cfg.AgentPreamble = cli.AgentPreamble
	}
	if cli.ExcludeDirs != nil {
		cfg.ExcludeDirs = cloneStrings(cli.ExcludeDirs)
	}
	if cli.Projects != nil {
		cfg.Projects = cloneStrings(cli.Projects)
	}
	if cli.ExcludeProjects != nil {
		cfg.ExcludeProjects = cloneStrings(cli.ExcludeProjects)
	}
	if cli.NeverRegister != nil {
		cfg.NeverRegister = cloneStrings(cli.NeverRegister)
	}
	if cli.SuffixMode != "" {
		cfg.SuffixMode = cli.SuffixMode
	}
	if cli.SuffixPrefix != "" {
		cfg.SuffixPrefix = cli.SuffixPrefix
	}
}

func validateConfig(cfg Config) error {
	if cfg.Version != configVersion {
		return fmt.Errorf("unsupported version %d (want %d)", cfg.Version, configVersion)
	}
	for alias, model := range cfg.ModelMap {
		if strings.TrimSpace(alias) == "" {
			return errors.New("modelMap contains an empty alias")
		}
		if strings.TrimSpace(model) == "" {
			return fmt.Errorf("modelMap[%q] is empty", alias)
		}
	}
	for _, dir := range cfg.ExcludeDirs {
		if err := validateRelativePath(dir, "excludeDirs entry"); err != nil {
			return err
		}
	}
	for _, project := range cfg.Projects {
		if strings.TrimSpace(project) == "" || filepath.IsAbs(project) || filepath.Clean(project) != project ||
			project == "." ||
			project == ".." ||
			strings.ContainsAny(project, `/\\`) {
			return fmt.Errorf("invalid projects entry %q", project)
		}
	}
	for _, project := range cfg.ExcludeProjects {
		if strings.TrimSpace(project) == "" || filepath.IsAbs(project) || filepath.Clean(project) != project ||
			project == "." ||
			project == ".." ||
			strings.ContainsAny(project, `/\\`) {
			return fmt.Errorf("invalid excludeProjects entry %q", project)
		}
	}
	switch cfg.SuffixMode {
	case "project", "strip-prefix", "none":
	default:
		return fmt.Errorf("suffixMode %q must be project, strip-prefix, or none", cfg.SuffixMode)
	}
	if cfg.SuffixMode == "strip-prefix" && cfg.SuffixPrefix == "" {
		return errors.New("suffixPrefix is required when suffixMode is strip-prefix")
	}
	return nil
}

func validateRelativePath(value, label string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s must not be empty", label)
	}
	clean := filepath.Clean(filepath.FromSlash(value))
	if filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+filepathSeparator) {
		return fmt.Errorf("%s %q must be relative and stay inside the repository", label, value)
	}
	return nil
}

// filepath separator is kept in a helper constant so the prefix check above
// remains readable while still handling Windows builds.
const filepathSeparator = string(filepath.Separator)

func cloneConfig(cfg Config) Config {
	cfg.ModelMap = cloneMap(cfg.ModelMap)
	cfg.ExcludeDirs = cloneStrings(cfg.ExcludeDirs)
	cfg.Projects = cloneStrings(cfg.Projects)
	cfg.ExcludeProjects = cloneStrings(cfg.ExcludeProjects)
	cfg.NeverRegister = cloneStrings(cfg.NeverRegister)
	return cfg
}

func cloneMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	return append([]string(nil), in...)
}
