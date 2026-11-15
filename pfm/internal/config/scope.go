package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// The scope keys: the part of pfm.config.json both scopes read. The machine file sets every key; a
// project's pfm.config.json, at its managed root, sets only these, laid over the machine's.
const (
	KeyEnvs           = "envs"
	KeyHarnessPrompt  = "harnessPrompt"
	KeyClaudeSettings = "claude.settings"
	keySettings       = "settings"
	keyClaudeHooks    = "hooks"
	keyClaudeEnv      = "env"
	harnessExtend     = "extend"
	harnessReplace    = "replace"
	// RetiredProjectFile is the project settings file pfm.config.json replaced; it is no longer read.
	RetiredProjectFile = "pfm.settings.json"
)

// HarnessPrompt is a scope's harnessPrompt: Key is "extend" or "replace", Relative the prompt file
// relative to Dir, the directory of the file that set it.
type HarnessPrompt struct {
	Key, Relative, Dir string
}

// Scope is the scope keys one pfm.config.json sets, each nil when unset.
type Scope struct {
	Path           string
	Envs           map[string]string
	HarnessPrompt  *HarnessPrompt
	ClaudeSettings map[string]any
}

type rawHarnessPrompt struct {
	Extend  string `json:"extend"`
	Replace string `json:"replace"`
}

// PathIn is the pfm.config.json of dir: the machine file in the source clone, a project's at its
// managed root.
func PathIn(dir string) string { return filepath.Join(dir, FileName) }

// EnvReserved reports a name pfm assigns on some launch door, several of them by inheritance (cx, the
// Herdr seat), where a configured value would land ahead of pfm's.
func EnvReserved(name string) bool {
	return strings.HasPrefix(name, "PFM_") || name == "CODEX_HOME" || name == "CLAUDE_CONFIG_DIR"
}

// DecodeClaudeSettings is body as Claude settings to lay over pfm's own: a JSON object whose hooks and
// env, when present, are objects, since any other shape would replace pfm's hooks or variables whole.
// source names the body in the error.
func DecodeClaudeSettings(body []byte, source string) (map[string]any, error) {
	var settings map[string]any
	if err := json.Unmarshal(body, &settings); err != nil || settings == nil {
		return nil, fmt.Errorf("%s: not a JSON object (%v)", source, err)
	}
	for _, key := range []string{keyClaudeHooks, keyClaudeEnv} {
		if value, present := settings[key]; present {
			if _, object := value.(map[string]any); !object {
				return nil, fmt.Errorf("%s: %q is not a JSON object", source, key)
			}
		}
	}
	return settings, nil
}

// decodeScope validates the scope keys raw sets: envs and claude.settings.env name no reserved
// variable, envs hold no NUL, harnessPrompt sets exactly one of extend and replace.
func decodeScope(raw rawConfig, path string) (Scope, error) {
	scope := Scope{Path: path}
	fault := func(err error) (Scope, error) { return Scope{}, fmt.Errorf("config %s: %w", path, err) }
	if claudeEnvSet(raw.Envs) {
		envs, err := DecodeEnv(KeyEnvs, raw.Envs)
		if err != nil {
			return fault(err)
		}
		for _, name := range slices.Sorted(maps.Keys(envs)) {
			if EnvReserved(name) {
				return fault(fmt.Errorf("%s.%s is set by pfm itself on launch", KeyEnvs, name))
			}
			if strings.ContainsRune(envs[name], 0) {
				return fault(fmt.Errorf("%s.%s must not contain NUL", KeyEnvs, name))
			}
		}
		scope.Envs = envs
	}
	if raw.Claude != nil && claudeEnvSet(raw.Claude.Settings) {
		settings, err := DecodeClaudeSettings(raw.Claude.Settings, KeyClaudeSettings)
		if err != nil {
			return fault(err)
		}
		// Claude applies settings env to its own process and every hook it runs: a reserved name
		// would redirect pfm's hooks as an envs entry would.
		env, _ := settings[keyClaudeEnv].(map[string]any)
		for _, name := range slices.Sorted(maps.Keys(env)) {
			if EnvReserved(name) {
				return fault(fmt.Errorf("%s.env.%s is set by pfm itself on launch", KeyClaudeSettings, name))
			}
		}
		scope.ClaudeSettings = settings
	}
	if raw.HarnessPrompt == nil {
		return scope, nil
	}
	prompt := HarnessPrompt{Key: harnessExtend, Relative: raw.HarnessPrompt.Extend, Dir: filepath.Dir(path)}
	switch {
	case prompt.Relative != "" && raw.HarnessPrompt.Replace != "":
		return fault(errors.New(`harnessPrompt sets both "extend" and "replace": keep one`))
	case prompt.Relative == "" && raw.HarnessPrompt.Replace == "":
		return fault(errors.New(`harnessPrompt needs "extend" or "replace"`))
	case prompt.Relative == "":
		prompt.Key, prompt.Relative = harnessReplace, raw.HarnessPrompt.Replace
	}
	scope.HarnessPrompt = &prompt
	return scope, nil
}

// ReadMachineScope is the scope keys of the machine file at path; an absent file sets none.
func ReadMachineScope(path string) (Scope, error) {
	content, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Scope{Path: path}, nil
	}
	if err != nil {
		return Scope{}, fmt.Errorf("read config %s: %w", path, err)
	}
	raw, err := decodeVersioned(path, content)
	if err != nil {
		return Scope{}, err
	}
	return decodeScope(raw, path)
}

// ReadProjectScope is the project scope of the managed root root: its pfm.config.json, read for the
// scope keys alone. When that file is the machine file at machinePath (os.SameFile), it is read as
// the machine's, and the project scope is empty. warnings name each machine-only or retired key the
// project file sets, never read, and a leftover pfm.settings.json, no longer read.
func ReadProjectScope(root, machinePath string) (scope Scope, warnings []string, err error) {
	path := PathIn(root)
	if _, statErr := os.Lstat(filepath.Join(root, RetiredProjectFile)); statErr == nil {
		warnings = append(warnings, fmt.Sprintf(
			"%s: %s is no longer read: move its keys into %s", root, RetiredProjectFile, FileName,
		))
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return Scope{}, warnings, fmt.Errorf("inspect %s: %w", filepath.Join(root, RetiredProjectFile), statErr)
	}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Scope{Path: path}, warnings, nil
	}
	if err != nil {
		return Scope{}, warnings, fmt.Errorf("config %s: inspect: %w", path, err)
	}
	// The machine file itself, or a link to it, is read as the machine's.
	if machinePath != "" {
		machine, err := os.Stat(machinePath)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return Scope{}, warnings, fmt.Errorf("config %s: inspect the machine file: %w", machinePath, err)
		}
		if target, targetErr := os.Stat(path); err == nil && targetErr == nil && os.SameFile(target, machine) {
			return Scope{Path: path}, warnings, nil
		}
	}
	// A FIFO would hang every launch; a link could leave the root.
	if !info.Mode().IsRegular() {
		return Scope{}, warnings, fmt.Errorf("config %s: not a regular file", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return Scope{}, warnings, fmt.Errorf("read config %s: %w", path, err)
	}
	raw, err := decodeVersioned(path, content)
	if err != nil {
		return Scope{}, warnings, err
	}
	warnings = append(warnings, projectOnlyWarnings(content, path)...)
	scope, err = decodeScope(raw, path)
	return scope, warnings, err
}

// applyScope sets the machine file's scope keys on result.
func applyScope(result *Config, raw rawConfig) error {
	scope, err := decodeScope(raw, result.Path)
	if err != nil {
		return err
	}
	result.Envs, result.HarnessPrompt, result.Claude.Settings = scope.Envs, scope.HarnessPrompt, scope.ClaudeSettings
	for key, set := range map[string]bool{
		KeyEnvs: scope.Envs != nil, KeyHarnessPrompt: scope.HarnessPrompt != nil,
		KeyClaudeSettings: scope.ClaudeSettings != nil,
	} {
		if set {
			result.Sources[key] = SourceFile
		}
	}
	result.Retired = retiredKeys(raw)
	return nil
}

// Scope is the machine file's scope keys.
func (config Config) Scope() Scope {
	return Scope{
		Path: config.Path, Envs: config.Envs, HarnessPrompt: config.HarnessPrompt,
		ClaudeSettings: config.Claude.Settings,
	}
}

// marshalScope adds the scope keys the machine file set to value, the top-level object, and claude,
// its claude block: an unset key stays out, so the written file keeps loading in a pfm that predates it.
func marshalScope(config Config, value, claude map[string]any) {
	if config.Envs != nil {
		value[KeyEnvs] = config.Envs
	}
	if prompt := config.HarnessPrompt; prompt != nil {
		value[KeyHarnessPrompt] = map[string]string{prompt.Key: prompt.Relative}
	}
	if config.Claude.Settings != nil {
		claude[keySettings] = config.Claude.Settings
	}
}
