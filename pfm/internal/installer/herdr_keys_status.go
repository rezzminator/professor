package installer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/BurntSushi/toml"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// herdrKeyMapping is one binding of the rendered keymap, as a doctor row
// names it when the config lacks it: the [keys] action or the
// [[keys.command]] key it binds.
type herdrKeyMapping struct{ action, key, name string }

// herdrKeyMappings is every binding keymap carries: each key of each action,
// then each command by its key.
func herdrKeyMappings(keymap herdrKeymap) []herdrKeyMapping {
	var mappings []herdrKeyMapping
	for _, action := range keymap.actions {
		for _, key := range action.binding.Keys {
			mappings = append(mappings, herdrKeyMapping{action.name, key, fmt.Sprintf("%s = %q", action.name, key)})
		}
	}
	for _, command := range keymap.commands {
		name := fmt.Sprintf("[[keys.command]] key = %q", command.Key)
		if command.Description != "" {
			name += " (" + command.Description + ")"
		}
		mappings = append(mappings, herdrKeyMapping{pfmconfig.HerdrKeysCommand, command.Key, name})
	}
	return mappings
}

// HerdrKeysStatus is the Herdr config's keymap as read from disk.
type HerdrKeysStatus struct {
	Path        string
	Exists      bool
	Block       bool     // pfm's marker block is present
	Current     bool     // the block holds the rendered keymap
	Want        string   // the block herdr.keys over pfm's keymap renders
	HandWritten bool     // a keys table is defined outside pfm's block
	Missing     []string // each pfm mapping the config does not bind
}

// Owned reports whether pfm's current block alone defines the keymap.
func (status HerdrKeysStatus) Owned() bool {
	return status.Block && status.Current && !status.HandWritten
}

// InspectHerdrKeys reads the Herdr config's keymap against herdr.keys
// (user, nil when unset) over pfm's keymap; a config that does not parse is
// an error, never a missing mapping.
func InspectHerdrKeys(home string, env paths.Env, user *pfmconfig.HerdrKeys) (HerdrKeysStatus, error) {
	path, err := paths.HerdrConfig(home, env)
	status := HerdrKeysStatus{Path: path}
	if err != nil {
		return status, err
	}
	keymap := herdrKeymapMerged(user)
	if status.Want, err = herdrKeysBlockFor(keymap); err != nil {
		return status, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		status.Missing = herdrKeysMissing(keymap, nil)
		return status, nil
	}
	if err != nil {
		return status, fmt.Errorf("read Herdr config %s: %w", path, err)
	}
	status.Exists = true
	whole, err := decodeHerdrConfig(string(raw))
	if err != nil {
		return status, fmt.Errorf("parse Herdr config %s: %w", path, err)
	}
	keys, _ := whole["keys"].(map[string]any)
	status.Missing = herdrKeysMissing(keymap, keys)
	outside := string(raw)
	if start, end, found := herdrKeysBlockRange(outside); found {
		status.Block = true
		status.Current = outside[start:end] == status.Want
		outside = outside[:start] + outside[end:]
	}
	rest, err := decodeHerdrConfig(outside)
	_, status.HandWritten = rest["keys"]
	if err != nil {
		// The block's own [keys] header is what the rest leans on: keys outside it.
		status.HandWritten = true
	}
	return status, nil
}

func decodeHerdrConfig(content string) (map[string]any, error) {
	config := map[string]any{}
	_, err := toml.Decode(content, &config)
	return config, err
}

// herdrKeysBlockRange finds pfm's keys block by its marker lines.
func herdrKeysBlockRange(content string) (start, end int, found bool) {
	start = strings.Index(content, herdrKeysOpen)
	if start < 0 || (start > 0 && content[start-1] != '\n') {
		return 0, 0, false
	}
	closeAt := strings.Index(content[start:], herdrKeysClose)
	if closeAt < 0 {
		return 0, 0, false
	}
	return start, start + closeAt + len(herdrKeysClose), true
}

// herdrKeysMissing names each mapping of keymap that keys does not bind; an
// action may be bound to one key or to a list of them.
func herdrKeysMissing(keymap herdrKeymap, keys map[string]any) []string {
	var missing []string
	for _, mapping := range herdrKeyMappings(keymap) {
		bound := false
		if mapping.action == pfmconfig.HerdrKeysCommand {
			for _, command := range herdrKeyCommands(keys[pfmconfig.HerdrKeysCommand]) {
				bound = bound || herdrKeyBinds(command["key"], mapping.key)
			}
		} else {
			bound = herdrKeyBinds(keys[mapping.action], mapping.key)
		}
		if !bound {
			missing = append(missing, mapping.name)
		}
	}
	return missing
}

func herdrKeyCommands(value any) []map[string]any {
	switch commands := value.(type) {
	case []map[string]any:
		return commands
	case []any:
		var out []map[string]any
		for _, command := range commands {
			if table, ok := command.(map[string]any); ok {
				out = append(out, table)
			}
		}
		return out
	}
	return nil
}

func herdrKeyBinds(value any, key string) bool {
	switch bound := value.(type) {
	case string:
		return bound == key
	case []any:
		for _, item := range bound {
			if item == key {
				return true
			}
		}
	}
	return false
}
