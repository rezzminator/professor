package config

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// KeyHerdr is the herdr block; KeyHerdrKeys names herdr.keys, Herdr's [keys]
// table as JSON, which pfm install merges over pfm's shipped keymap.
// Machine-wide: an account entry has no herdr key, so the strict decoder
// refuses one, and a project file's is named as never read.
const (
	KeyHerdr     = "herdr"
	KeyHerdrKeys = KeyHerdr + ".keys"
	// KeyHerdrSettings names herdr.settings, Herdr's config.toml as JSON.
	KeyHerdrSettings = KeyHerdr + ".settings"
	// HerdrKeysCommand is the [keys] array of [[keys.command]] tables, the
	// one name every reader of the keymap uses.
	HerdrKeysCommand = "command"
)

// herdrKeyCommandFields are the fields of a [[keys.command]] table, in the
// order their checks run, and as a refusal names them.
var herdrKeyCommandFields = []string{"key", "type", "description", "command"}

const herdrKeyCommandFieldsText = "key, type, description or command"

// Herdr is the resolved herdr block.
type Herdr struct {
	// Keys is herdr.keys, nil when unset.
	Keys *HerdrKeys
	// Settings is herdr.settings, nil when unset: Herdr's config.toml as JSON,
	// an object a table, null removing pfm's default of that key.
	Settings map[string]any
}

// HerdrKeys is the user's Herdr keymap. Each action's binding replaces pfm's
// binding of that action, a nil binding removing it; each command entry
// replaces pfm's command of the same key, or adds one, and an entry holding
// only its key (Remove) removes pfm's command of that key.
type HerdrKeys struct {
	Actions  map[string]*HerdrKeyBinding
	Commands []HerdrKeyCommand
}

// HerdrKeyBinding is one action's keys: a single key unless List.
type HerdrKeyBinding struct {
	Keys []string
	List bool
}

// HerdrKeyCommand is one [[keys.command]] table, its TOML shape the one pfm
// install renders.
type HerdrKeyCommand struct {
	Key         string `toml:"key"`
	Type        string `toml:"type"`
	Description string `toml:"description,omitempty"`
	Command     string `toml:"command"`
	Remove      bool   `toml:"-"`
}

type rawHerdr struct {
	Keys     json.RawMessage `json:"keys,omitempty"`
	Settings json.RawMessage `json:"settings,omitempty"`
}

// applyHerdr decodes herdr.keys and herdr.settings; absent and null leave
// each unset.
func applyHerdr(result *Config, raw *rawHerdr) error {
	if raw == nil {
		return nil
	}
	if err := applyHerdrSettings(result, raw.Settings); err != nil {
		return err
	}
	if !claudeEnvSet(raw.Keys) {
		return nil
	}
	keys, err := decodeHerdrKeys(raw.Keys)
	if err != nil {
		return fmt.Errorf("config %s: %w", result.Path, err)
	}
	result.Herdr.Keys = &keys
	result.Sources[KeyHerdrKeys] = SourceFile
	return nil
}

func decodeHerdrKeys(raw json.RawMessage) (HerdrKeys, error) {
	var table map[string]json.RawMessage
	if err := json.Unmarshal(raw, &table); err != nil {
		return HerdrKeys{}, fmt.Errorf("%s must be an object of Herdr actions", KeyHerdrKeys)
	}
	keys := HerdrKeys{Actions: map[string]*HerdrKeyBinding{}}
	for _, action := range slices.Sorted(maps.Keys(table)) {
		path := KeyHerdrKeys + "." + action
		if action == HerdrKeysCommand {
			commands, err := decodeHerdrKeyCommands(path, table[action])
			if err != nil {
				return HerdrKeys{}, err
			}
			keys.Commands = commands
			continue
		}
		binding, err := decodeHerdrKeyBinding(path, table[action])
		if err != nil {
			return HerdrKeys{}, err
		}
		keys.Actions[action] = binding
	}
	return keys, nil
}

func decodeHerdrKeyBinding(path string, raw json.RawMessage) (*HerdrKeyBinding, error) {
	if jsonNull(raw) {
		return nil, nil
	}
	if key, ok := jsonString(raw); ok {
		if strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("%s must not be blank: null removes the binding", path)
		}
		return &HerdrKeyBinding{Keys: []string{key}}, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("%s must be a key, an array of keys or null", path)
	}
	binding := &HerdrKeyBinding{Keys: make([]string, 0, len(items)), List: true}
	for index, item := range items {
		key, ok := jsonString(item)
		if !ok {
			return nil, fmt.Errorf("%s[%d] must be a string", path, index)
		}
		if strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("%s[%d] must not be blank", path, index)
		}
		binding.Keys = append(binding.Keys, key)
	}
	return binding, nil
}

func decodeHerdrKeyCommands(path string, raw json.RawMessage) ([]HerdrKeyCommand, error) {
	var items []json.RawMessage
	if jsonNull(raw) || json.Unmarshal(raw, &items) != nil {
		return nil, fmt.Errorf("%s must be an array of command tables", path)
	}
	commands := make([]HerdrKeyCommand, 0, len(items))
	first := map[string]int{}
	for index, item := range items {
		entry := fmt.Sprintf("%s[%d]", path, index)
		var fields map[string]json.RawMessage
		if jsonNull(item) || json.Unmarshal(item, &fields) != nil {
			return nil, fmt.Errorf("%s must be an object", entry)
		}
		for _, name := range slices.Sorted(maps.Keys(fields)) {
			if !slices.Contains(herdrKeyCommandFields, name) {
				return nil, fmt.Errorf("%s.%s is not a command field: use %s", entry, name, herdrKeyCommandFieldsText)
			}
		}
		values := map[string]string{}
		for _, name := range herdrKeyCommandFields {
			raw, set := fields[name]
			if !set {
				continue
			}
			value, ok := jsonString(raw)
			if !ok {
				return nil, fmt.Errorf("%s.%s must be a string", entry, name)
			}
			if name != "description" && strings.TrimSpace(value) == "" {
				return nil, fmt.Errorf("%s.%s must not be blank", entry, name)
			}
			values[name] = value
		}
		key, set := values["key"]
		if !set {
			return nil, fmt.Errorf("%s.key is required", entry)
		}
		if earlier, repeated := first[key]; repeated {
			return nil, fmt.Errorf("%s.key %q repeats %s[%d]", entry, key, path, earlier)
		}
		first[key] = index
		command := HerdrKeyCommand{Key: key, Remove: len(fields) == 1}
		if !command.Remove {
			for _, name := range []string{"type", "command"} {
				if _, set := values[name]; !set {
					return nil, fmt.Errorf(
						"%s.%s is required (an entry holding only key removes pfm's command of that key)", entry, name)
				}
			}
			command.Type, command.Description, command.Command = values["type"], values["description"], values["command"]
		}
		commands = append(commands, command)
	}
	return commands, nil
}

// JSON is keys in its herdr.keys file shape.
func (keys HerdrKeys) JSON() map[string]any {
	value := make(map[string]any, len(keys.Actions)+1)
	for action, binding := range keys.Actions {
		switch {
		case binding == nil:
			value[action] = nil
		case binding.List:
			value[action] = binding.Keys
		default:
			value[action] = binding.Keys[0]
		}
	}
	if keys.Commands == nil {
		return value
	}
	commands := make([]map[string]string, 0, len(keys.Commands))
	for _, command := range keys.Commands {
		entry := map[string]string{"key": command.Key}
		if !command.Remove {
			entry["type"], entry["command"] = command.Type, command.Command
			if command.Description != "" {
				entry["description"] = command.Description
			}
		}
		commands = append(commands, entry)
	}
	value[HerdrKeysCommand] = commands
	return value
}
