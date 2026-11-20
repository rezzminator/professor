package installer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// herdrKeysCommand is the [keys] array of [[keys.command]] tables.
const herdrKeysCommand = "command"

// herdrKeyMappings is each binding pfm's keymap carries, as a doctor row
// names it when the config lacks it: the [keys] action or the
// [[keys.command]] key it binds.
var herdrKeyMappings = []struct{ action, key, name string }{
	{action: "previous_workspace", key: "prefix+i", name: `previous_workspace = "prefix+i"`},
	{action: "next_workspace", key: "prefix+m", name: `next_workspace = "prefix+m"`},
	{action: herdrKeysCommand, key: "prefix+a", name: `[[keys.command]] key = "prefix+a" (new pfm chat in this space)`},
}

// HerdrKeysStatus is the Herdr config's keymap as read from disk.
type HerdrKeysStatus struct {
	Path        string
	Exists      bool
	Block       bool     // pfm's marker block is present
	Current     bool     // the block holds this pfm's keymap
	HandWritten bool     // a keys table is defined outside pfm's block
	Missing     []string // each pfm mapping the config does not bind
}

// Owned reports whether pfm's current block alone defines the keymap.
func (status HerdrKeysStatus) Owned() bool {
	return status.Block && status.Current && !status.HandWritten
}

// InspectHerdrKeys reads the Herdr config's keymap; a config that does not
// parse is an error, never a missing mapping.
func InspectHerdrKeys(home string, env paths.Env) (HerdrKeysStatus, error) {
	path, err := paths.HerdrConfig(home, env)
	status := HerdrKeysStatus{Path: path}
	if err != nil {
		return status, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		status.Missing = herdrKeysMissing(nil)
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
	status.Missing = herdrKeysMissing(keys)
	outside := string(raw)
	if start, end, found := herdrKeysBlockRange(outside); found {
		status.Block = true
		status.Current = outside[start:end] == herdrKeysBlock
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

// herdrKeysMissing names each pfm mapping keys does not bind; an action may
// be bound to one key or to a list of them.
func herdrKeysMissing(keys map[string]any) []string {
	var missing []string
	for _, mapping := range herdrKeyMappings {
		bound := false
		if mapping.action == herdrKeysCommand {
			for _, command := range herdrKeyCommands(keys[herdrKeysCommand]) {
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
