package claudelaunch

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// defaultsJSON holds the plain values pfm sets for Claude, as data: settings is a Claude settings
// object every launch carries beneath pfm's computed keys, registry the preferences Claude reads only
// from an account's .claude.json, which pfm install writes, userSettings the settings keys pfm install
// also writes into each account's own settings.json, and reasons say why each value is set.
//
//go:embed defaults.json
var defaultsJSON []byte

const (
	defaultsSettings = "settings"
	defaultsRegistry = "registry"
	defaultsUser     = "userSettings"
)

// defaultValue is one default: its key, its compact JSON value and why it is set.
type defaultValue struct {
	key    string
	value  json.RawMessage
	reason string
}

// launchDefaults is defaults.json decoded, each list sorted by key: settings holds the top-level
// settings keys but env, whose variables env holds.
type launchDefaults struct {
	settings, env, registry, userSettings []defaultValue
}

var defaults = func() launchDefaults {
	parsed, err := parseDefaults(defaultsJSON)
	if err != nil {
		panic(fmt.Sprintf("claudelaunch: embedded defaults.json: %v", err))
	}
	return parsed
}()

// parseDefaults decodes a defaults file, refusing an unknown key, a settings env that is not an
// object of strings, a user setting naming no top-level settings value, a value without a reason and a
// reason naming no value.
func parseDefaults(raw []byte) (launchDefaults, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var file struct {
		Settings map[string]json.RawMessage `json:"settings"`
		Registry map[string]json.RawMessage `json:"registry"`
		User     []string                   `json:"userSettings"`
		Reasons  map[string]string          `json:"reasons"`
	}
	if err := decoder.Decode(&file); err != nil {
		return launchDefaults{}, fmt.Errorf("decode: %w", err)
	}
	reasons := maps.Clone(file.Reasons)
	collect := func(prefix string, values map[string]json.RawMessage) ([]defaultValue, error) {
		var list []defaultValue
		for _, key := range slices.Sorted(maps.Keys(values)) {
			var compact bytes.Buffer
			if err := json.Compact(&compact, values[key]); err != nil {
				return nil, fmt.Errorf("%s%s: %w", prefix, key, err)
			}
			reason := reasons[prefix+key]
			if reason == "" {
				return nil, fmt.Errorf("%s%s has no reason", prefix, key)
			}
			delete(reasons, prefix+key)
			list = append(list, defaultValue{key, compact.Bytes(), reason})
		}
		return list, nil
	}
	var parsed launchDefaults
	envRaw, hasEnv := file.Settings[settingsEnvKey]
	delete(file.Settings, settingsEnvKey)
	var err error
	if parsed.settings, err = collect(defaultsSettings+".", file.Settings); err != nil {
		return launchDefaults{}, err
	}
	if hasEnv {
		var env map[string]any
		if err := json.Unmarshal(envRaw, &env); err != nil || env == nil {
			return launchDefaults{}, fmt.Errorf("settings.env is not an object (%v)", err)
		}
		values := make(map[string]json.RawMessage, len(env))
		for name, value := range env {
			if _, text := value.(string); !text {
				return launchDefaults{}, fmt.Errorf("settings.env.%s is not a string", name)
			}
			if values[name], err = json.Marshal(value); err != nil {
				return launchDefaults{}, fmt.Errorf("settings.env.%s: %w", name, err)
			}
		}
		if parsed.env, err = collect(defaultsSettings+"."+settingsEnvKey+".", values); err != nil {
			return launchDefaults{}, err
		}
	}
	if parsed.registry, err = collect(defaultsRegistry+".", file.Registry); err != nil {
		return launchDefaults{}, err
	}
	user := map[string]json.RawMessage{}
	for _, key := range file.User {
		if _, present := file.Settings[key]; !present {
			return launchDefaults{}, fmt.Errorf("%s.%s names no top-level settings value", defaultsUser, key)
		}
		user[key] = file.Settings[key]
	}
	if parsed.userSettings, err = collect(defaultsUser+".", user); err != nil {
		return launchDefaults{}, err
	}
	if len(reasons) != 0 {
		return launchDefaults{}, fmt.Errorf("reasons name no value: %v", slices.Sorted(maps.Keys(reasons)))
	}
	return parsed, nil
}

// decode is value as Go: a JSON string, number, bool, object or array.
func (value defaultValue) decode() any {
	var decoded any
	if err := json.Unmarshal(value.value, &decoded); err != nil {
		// parseDefaults compacted every value, so only memory corruption reaches this.
		panic(fmt.Sprintf("claudelaunch: default %s: %v", value.key, err))
	}
	return decoded
}

// defaultSettings is a fresh copy of the default settings, the env block apart, for a launch to
// build its own on.
func defaultSettings() (settings map[string]any, env map[string]string) {
	settings = make(map[string]any, len(defaults.settings))
	for _, value := range defaults.settings {
		settings[value.key] = value.decode()
	}
	env = make(map[string]string, len(defaults.env))
	for _, value := range defaults.env {
		env[value.key] = value.decode().(string)
	}
	return settings, env
}

// defaultKnobs are the registry rows of the default settings.
func defaultKnobs() []Knob {
	rows := make([]Knob, 0, len(defaults.settings)+len(defaults.env))
	for _, value := range defaults.settings {
		rows = append(rows, Knob{value.key, WireSettings, value.key, SourceConstant, value.decode(), value.reason})
	}
	for _, value := range defaults.env {
		target := settingsEnvKey + "." + value.key
		rows = append(rows, Knob{target, WireSettings, target, SourceConstant, value.decode(), value.reason})
	}
	return rows
}

// DefaultSettingKeys are the top-level --settings keys every launch carries from the defaults, env
// apart: a project's or caller's settings can change their values but never drop them.
func DefaultSettingKeys() []string {
	keys := make([]string, 0, len(defaults.settings))
	for _, value := range defaults.settings {
		keys = append(keys, value.key)
	}
	return keys
}

// RegistryDefault is one preference Claude Code keeps in an account's registry (.claude.json)
// rather than its settings, so no --settings can set it: pfm install writes it.
type RegistryDefault struct {
	Key    string
	Value  json.RawMessage // compact JSON
	Reason string
}

// RegistryDefaults are the defaults file's registry values, sorted by key.
func RegistryDefaults() []RegistryDefault {
	list := make([]RegistryDefault, 0, len(defaults.registry))
	for _, value := range defaults.registry {
		list = append(list, RegistryDefault{value.key, slices.Clone(value.value), value.reason})
	}
	return list
}

// EffectiveSettings is claude, the layered claude.settings, over the shipped defaults: the value a
// launch's --settings carries for every key pfm does not compute. A consumer outside that wire (the
// passthrough session's environment, the managed cleanup drop-in) reads its value here.
func EffectiveSettings(claude map[string]any) map[string]any {
	settings, env := defaultSettings()
	settings[settingsEnvKey] = env
	layered, err := LayerSettings(claude)
	if err != nil {
		// The shipped defaults stand; the launch's own --settings refuses the same layer.
		obs.Logger(context.Background()).Error("claudelaunch: layer claude.settings", obs.FieldErr, err.Error())
		return settings
	}
	mergeOwnerSettings(settings, layered)
	dropNullSettings(settings)
	return settings
}

// EffectiveEnv is EffectiveSettings' env block, each value as Claude reads it: a string kept, a null
// left out, any other JSON value in its JSON text.
func EffectiveEnv(claude map[string]any) map[string]string {
	result := map[string]string{}
	switch env := EffectiveSettings(claude)[settingsEnvKey].(type) {
	case map[string]string:
		for name, value := range env {
			result[name] = value
		}
	case map[string]any:
		for name, value := range env {
			if text, isText := value.(string); isText {
				result[name] = text
				continue
			}
			if value == nil {
				continue
			}
			encoded, err := json.Marshal(value)
			if err != nil {
				continue // a value json.Unmarshal produced always marshals
			}
			result[name] = string(encoded)
		}
	}
	return result
}
