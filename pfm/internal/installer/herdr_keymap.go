package installer

import (
	"bytes"
	"fmt"
	"maps"
	"slices"

	"github.com/BurntSushi/toml"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// herdrKeymap is a Herdr [keys] table in order: pfm's actions and commands
// first, then the user's additions.
type herdrKeymap struct {
	actions  []herdrKeyAction
	commands []pfmconfig.HerdrKeyCommand
}

type herdrKeyAction struct {
	name    string
	binding pfmconfig.HerdrKeyBinding
}

// herdrKeymapDefault is pfm's shipped keymap: prefix+i / prefix+m walk the
// spaces (VS Code's cmd+shift+up/down send ^Bi / ^Bm into the terminal),
// prefix+a opens a pfm chat in the active space.
func herdrKeymapDefault() herdrKeymap {
	return herdrKeymap{
		actions: []herdrKeyAction{
			{name: "previous_workspace", binding: pfmconfig.HerdrKeyBinding{Keys: []string{"prefix+i"}}},
			{name: "next_workspace", binding: pfmconfig.HerdrKeyBinding{Keys: []string{"prefix+m"}}},
		},
		commands: []pfmconfig.HerdrKeyCommand{
			{
				Key:         "prefix+a",
				Type:        "shell",
				Description: "new pfm chat in this space",
				Command: `"${HERDR_BIN_PATH:-herdr}" tab create --workspace "${HERDR_ACTIVE_WORKSPACE_ID:-$HERDR_WORKSPACE_ID}" ` +
					`--env PFM_AUTO_OPEN=pfm --focus`,
			},
		},
	}
}

// herdrKeymapMerged is herdr.keys over pfm's keymap: a user action replaces
// pfm's binding of it, null removing it; a user command replaces pfm's of the
// same key, an entry holding only its key removing it; the rest is added,
// actions by name, commands in the user's order. Unset, it is pfm's keymap.
func herdrKeymapMerged(user *pfmconfig.HerdrKeys) herdrKeymap {
	keymap := herdrKeymapDefault()
	if user == nil {
		return keymap
	}
	merged := herdrKeymap{}
	isDefault := map[string]bool{}
	for _, action := range keymap.actions {
		isDefault[action.name] = true
		binding, set := user.Actions[action.name]
		switch {
		case !set:
			merged.actions = append(merged.actions, action)
		case binding != nil:
			merged.actions = append(merged.actions, herdrKeyAction{name: action.name, binding: *binding})
		}
	}
	for _, name := range slices.Sorted(maps.Keys(user.Actions)) {
		if binding := user.Actions[name]; binding != nil && !isDefault[name] {
			merged.actions = append(merged.actions, herdrKeyAction{name: name, binding: *binding})
		}
	}
	isDefault = map[string]bool{}
	for _, command := range keymap.commands {
		isDefault[command.Key] = true
		index := slices.IndexFunc(user.Commands, func(entry pfmconfig.HerdrKeyCommand) bool {
			return entry.Key == command.Key
		})
		switch {
		case index < 0:
			merged.commands = append(merged.commands, command)
		case !user.Commands[index].Remove:
			merged.commands = append(merged.commands, user.Commands[index])
		}
	}
	for _, command := range user.Commands {
		if !command.Remove && !isDefault[command.Key] {
			merged.commands = append(merged.commands, command)
		}
	}
	return merged
}

// herdrKeysBlockFor renders keymap as pfm's marked block, the one door to its
// TOML: sorted actions, then each [[keys.command]] in the keymap's order.
// TOML comments are not carried.
func herdrKeysBlockFor(keymap herdrKeymap) (string, error) {
	table := make(map[string]any, len(keymap.actions)+1)
	for _, action := range keymap.actions {
		if action.binding.List {
			table[action.name] = action.binding.Keys
		} else {
			table[action.name] = action.binding.Keys[0]
		}
	}
	if len(keymap.commands) != 0 {
		table[pfmconfig.HerdrKeysCommand] = keymap.commands
	}
	var body bytes.Buffer
	encoder := toml.NewEncoder(&body)
	encoder.Indent = ""
	if err := encoder.Encode(map[string]any{"keys": table}); err != nil {
		return "", fmt.Errorf("render pfm's Herdr keys: %w", err)
	}
	return herdrKeysOpen + body.String() + herdrKeysClose, nil
}
