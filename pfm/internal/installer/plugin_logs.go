package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// PluginLogDirEnv is the settings.json env key naming the folder the
// marketplace plugins write their logs under, {value}/{plugin}/{errors,
// warnings,info}.jsonl; with it unset each plugin falls back to
// ~/.local/state/{plugin}/logs/.
const PluginLogDirEnv = "PLUGIN_LOG_DIR"

// PluginLogFallbackDir is the folder plugin writes its log rows under while
// PluginLogDirEnv is unset, as the plugin resolves it: callmeter's home's
// logs/ (callmeterHome, which env's CALLMETER_HOME and an absolute
// XDG_STATE_HOME move), every other plugin's home/.local/state/{plugin}/logs.
func PluginLogFallbackDir(env paths.Env, home, plugin string) (string, error) {
	if plugin == callmeterPlugin {
		root, err := callmeterHome(env, home)
		if err != nil {
			return "", err
		}
		return filepath.Join(root, "logs"), nil
	}
	return filepath.Join(home, ".local", "state", plugin, "logs"), nil
}

// PluginLogEnvSetting is one Claude config dir's PluginLogDirEnv. Settings is
// its settings.json path and Physical the file that path resolves to: config
// dirs whose settings.json links to one file share it. Absent says the file,
// its env object or the key is not there, or the value is empty; Err says the
// file could not be read or parsed, or env or the value is not the JSON type
// Claude Code reads — never an absent value.
type PluginLogEnvSetting struct {
	Dir      string
	Settings string
	Physical string
	Value    string
	Absent   bool
	Err      error
}

// ReadPluginLogEnv reads PluginLogDirEnv from the settings.json of every dir,
// in order, a dir named twice once. Each physical file is read once and its
// answer given to every dir resolving to it.
func ReadPluginLogEnv(dirs []string) []PluginLogEnvSetting {
	type answer struct {
		value  string
		absent bool
		err    error
	}
	read := map[string]answer{}
	seen := map[string]bool{}
	settings := make([]PluginLogEnvSetting, 0, len(dirs))
	for _, dir := range dirs {
		dir = filepath.Clean(dir)
		if seen[dir] {
			continue
		}
		seen[dir] = true
		setting := PluginLogEnvSetting{Dir: dir, Settings: filepath.Join(dir, "settings.json")}
		setting.Physical = setting.Settings
		if _, err := os.Lstat(setting.Settings); errors.Is(err, fs.ErrNotExist) {
			setting.Absent = true
			settings = append(settings, setting)
			continue
		} else if err != nil {
			setting.Err = fmt.Errorf("inspect %s: %w", setting.Settings, err)
			settings = append(settings, setting)
			continue
		}
		physical, err := filepath.EvalSymlinks(setting.Settings)
		if err != nil {
			setting.Err = fmt.Errorf("resolve %s: %w", setting.Settings, err)
			settings = append(settings, setting)
			continue
		}
		setting.Physical = physical
		got, ok := read[physical]
		if !ok {
			got.value, got.absent, got.err = readPluginLogDir(physical)
			read[physical] = got
		}
		setting.Value, setting.Absent, setting.Err = got.value, got.absent, got.err
		settings = append(settings, setting)
	}
	return settings
}

// readPluginLogDir is the PluginLogDirEnv value in the settings file at path,
// which must be a regular file: a FIFO or a device would block or never end.
func readPluginLogDir(path string) (string, bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", false, fmt.Errorf("inspect %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("%s is not a regular file", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false, fmt.Errorf("read %s: %w", path, err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		return "", false, fmt.Errorf("parse %s: %w", path, err)
	}
	envRaw, ok := document["env"]
	if !ok || string(envRaw) == "null" {
		return "", true, nil
	}
	var env map[string]json.RawMessage
	if err := json.Unmarshal(envRaw, &env); err != nil {
		return "", false, fmt.Errorf("%s: env is not an object: %w", path, err)
	}
	valueRaw, ok := env[PluginLogDirEnv]
	if !ok || string(valueRaw) == "null" {
		return "", true, nil
	}
	var value string
	if err := json.Unmarshal(valueRaw, &value); err != nil {
		return "", false, fmt.Errorf("%s: env.%s is not a string: %w", path, PluginLogDirEnv, err)
	}
	if value == "" {
		return "", true, nil
	}
	return value, false, nil
}
