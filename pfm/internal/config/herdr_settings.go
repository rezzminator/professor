package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// applyHerdrSettings decodes herdr.settings; absent and null leave it unset.
func applyHerdrSettings(result *Config, raw json.RawMessage) error {
	if !claudeEnvSet(raw) {
		return nil
	}
	settings, err := DecodeHerdrSettings(raw)
	if err != nil {
		return fmt.Errorf("config %s: %w", result.Path, err)
	}
	result.Herdr.Settings = settings
	result.Sources[KeyHerdrSettings] = SourceFile
	return nil
}

// DecodeHerdrSettings decodes a herdr.settings object, numbers kept as
// json.Number so an integer stays a TOML integer. An object is a table, null
// removes pfm's default; it refuses keys, Herdr's keymap, which herdr.keys
// owns, and an array holding a table or null, which a TOML key cannot hold.
func DecodeHerdrSettings(raw json.RawMessage) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var settings map[string]any
	if err := decoder.Decode(&settings); err != nil || settings == nil {
		return nil, fmt.Errorf("%s must be an object of Herdr config.toml keys", KeyHerdrSettings)
	}
	if _, set := settings["keys"]; set {
		return nil, fmt.Errorf("%s.keys is Herdr's keymap: set it in %s", KeyHerdrSettings, KeyHerdrKeys)
	}
	return settings, checkHerdrSettings(KeyHerdrSettings, settings)
}

func checkHerdrSettings(path string, table map[string]any) error {
	for _, key := range slices.Sorted(maps.Keys(table)) {
		name := path + "." + key
		if table, isTable := table[key].(map[string]any); isTable {
			if err := checkHerdrSettings(name, table); err != nil {
				return err
			}
			continue
		}
		if err := checkHerdrSettingValue(name, table[key]); err != nil {
			return err
		}
	}
	return nil
}

// checkHerdrSettingValue refuses, at any array depth, a table or null inside an
// array, and an integer a TOML integer (64-bit) cannot hold.
func checkHerdrSettingValue(name string, value any) error {
	switch value := value.(type) {
	case json.Number:
		if !strings.ContainsAny(value.String(), ".eE") {
			if _, err := value.Int64(); err != nil {
				return fmt.Errorf("%s must be an integer TOML can hold (64-bit), not %s", name, value)
			}
		}
	case []any:
		for index := range value {
			item := fmt.Sprintf("%s[%d]", name, index)
			switch value[index].(type) {
			case map[string]any, nil:
				return fmt.Errorf("%s must be a string, number, boolean or array", item)
			}
			if err := checkHerdrSettingValue(item, value[index]); err != nil {
				return err
			}
		}
	}
	return nil
}

// marshalHerdr is the herdr block, nil while herdr.keys and herdr.settings
// are unset so a file without them stays readable by a pfm that predates them.
func marshalHerdr(config Config) map[string]any {
	value := map[string]any{}
	if config.Herdr.Keys != nil {
		value["keys"] = config.Herdr.Keys.JSON()
	}
	if config.Herdr.Settings != nil {
		value["settings"] = config.Herdr.Settings
	}
	if len(value) == 0 {
		return nil
	}
	return value
}

// HerdrSummary renders herdr.keys and herdr.settings, each with its source,
// for `pfm config show`.
func (config Config) HerdrSummary() string {
	var keys, settings any
	if config.Herdr.Keys != nil {
		keys = config.Herdr.Keys.JSON()
	}
	if config.Herdr.Settings != nil {
		settings = config.Herdr.Settings
	}
	var summary string
	for _, row := range []struct {
		key   string
		value any
	}{{KeyHerdrKeys, keys}, {KeyHerdrSettings, settings}} {
		text := "none"
		if row.value != nil {
			encoded, err := json.Marshal(row.value)
			text = string(encoded)
			if err != nil {
				text = fmt.Sprintf("unencodable (%v)", err)
			}
		}
		summary += fmt.Sprintf("config %s=%s (%s)\n", row.key, text, config.Source(row.key))
	}
	return summary
}
