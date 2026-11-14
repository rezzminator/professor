package claudelaunch

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// settingsEnvKey is the --settings key holding the launch's environment variables.
const settingsEnvKey = "env"

// settingsArgv folds every --settings the caller typed (a file or inline JSON) into pfm's own settings
// and returns the caller's other words with the one --settings value Claude reads. Claude takes a single
// --settings, the last one winning: appended behind `claude --settings ~/mine.json`, pfm's own would
// silently discard the caller's file. So the caller's keys win over pfm's, except env, merged variable by
// variable (the caller's value winning), and hooks, where each event runs pfm's hooks then the caller's.
// A caller's settings that cannot be read, are not a JSON object or hold a hooks or env that is not one
// refuse the launch, naming them.
func settingsArgv(args []string, settings map[string]any) (rest []string, payload string, err error) {
	rest = make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		word := args[index]
		if word == "--" {
			rest = append(rest, args[index:]...)
			break
		}
		value, inline := strings.CutPrefix(word, flagSettings+"=")
		if !inline {
			if word != flagSettings || index+1 >= len(args) {
				rest = append(rest, word)
				continue
			}
			index++
			value = args[index]
		}
		owner, readErr := ownerSettings(value)
		if readErr != nil {
			return nil, "", readErr
		}
		mergeOwnerSettings(settings, owner)
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		return nil, "", fmt.Errorf("render --settings: %w", err)
	}
	return rest, string(encoded), nil
}

// ownerSettings is the JSON object a caller's --settings value names: inline JSON, or a file holding it.
func ownerSettings(value string) (map[string]any, error) {
	body, source := []byte(value), "inline JSON"
	if !strings.HasPrefix(strings.TrimSpace(value), "{") {
		read, err := os.ReadFile(value)
		if err != nil {
			return nil, fmt.Errorf("%s %s: read the settings file: %w", flagSettings, value, err)
		}
		body, source = read, value
	}
	var owner map[string]any
	if err := json.Unmarshal(body, &owner); err != nil || owner == nil {
		return nil, fmt.Errorf("%s %s: not a JSON object (%v)", flagSettings, source, err)
	}
	// hooks and env merge into pfm's own: any other shape would replace pfm's hooks or variables whole.
	for _, key := range []string{knobHooks, settingsEnvKey} {
		if value, present := owner[key]; present {
			if _, object := value.(map[string]any); !object {
				return nil, fmt.Errorf("%s %s: %q is not a JSON object", flagSettings, source, key)
			}
		}
	}
	return owner, nil
}

// mergeOwnerSettings lays owner over settings as settingsArgv describes.
func mergeOwnerSettings(settings, owner map[string]any) {
	for key, value := range owner {
		mine, ours := value.(map[string]any)
		theirs, held := settings[key].(map[string]any)
		switch {
		case key == knobHooks && ours && held:
			for event, entries := range mine {
				pfmEntries, _ := theirs[event].([]any)
				ownerEntries, _ := entries.([]any)
				theirs[event] = append(append([]any(nil), pfmEntries...), ownerEntries...)
			}
		case key == settingsEnvKey && ours && held:
			for name, variable := range mine {
				theirs[name] = variable
			}
		case key == settingsEnvKey && ours:
			if pfmEnv, typed := settings[key].(map[string]string); typed {
				merged := make(map[string]any, len(pfmEnv)+len(mine))
				for name, variable := range pfmEnv {
					merged[name] = variable
				}
				for name, variable := range mine {
					merged[name] = variable
				}
				settings[key] = merged
				continue
			}
			settings[key] = value
		default:
			settings[key] = value
		}
	}
}
