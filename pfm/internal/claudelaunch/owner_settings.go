package claudelaunch

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// settingsEnvKey is the --settings key holding the launch's environment variables.
const settingsEnvKey = "env"

// settingsArgv folds the project's claude.settings, then every --settings the caller typed (a file or
// inline JSON), into pfm's own settings and returns the caller's other words with the one --settings
// value Claude reads. Claude takes a single --settings, the last one winning: appended behind
// `claude --settings ~/mine.json`, pfm's own would silently discard the caller's file. So the project's
// keys win over pfm's and the caller's over both, except env, merged variable by variable (the later
// value winning), and hooks, where each event runs pfm's hooks, then the project's, then the caller's.
// A caller's settings that cannot be read, are not a JSON object or hold a hooks or env that is not one
// refuse the launch, naming them.
func settingsArgv(args []string, project, settings map[string]any) (rest []string, payload string, err error) {
	project, err = cloneOwnerSettings(project)
	if err != nil {
		return nil, "", err
	}
	mergeOwnerSettings(settings, project)
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
	dropNullEnv(settings)
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
	return DecodeOwnerSettings(body, flagSettings+" "+source)
}

// DecodeOwnerSettings is body as settings to lay over pfm's own (pfmconfig.DecodeClaudeSettings).
func DecodeOwnerSettings(body []byte, source string) (map[string]any, error) {
	return pfmconfig.DecodeClaudeSettings(body, source)
}

// LayerSettings is layers laid over one another in order, as settingsArgv lays a caller's settings
// (mergeOwnerSettings), into a copy: the machine's claude.settings, then the project's. Nil when no
// layer sets a key.
func LayerSettings(layers ...map[string]any) (map[string]any, error) {
	var result map[string]any
	for _, layer := range layers {
		clone, err := cloneOwnerSettings(layer)
		if err != nil {
			return nil, err
		}
		if clone == nil {
			continue
		}
		if result == nil {
			result = clone
			continue
		}
		mergeOwnerSettings(result, clone)
	}
	return result, nil
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
	dropNullEnv(settings)
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

// CleanupPeriodDays is the effective cleanupPeriodDays under claude: a positive whole number of days,
// else an error naming the value.
func CleanupPeriodDays(claude map[string]any) (int, error) {
	value := EffectiveSettings(claude)[knobCleanupPeriodDays]
	if days, ok := integer(value); ok && days >= 1 && days <= math.MaxInt32 {
		return int(days), nil
	}
	return 0, fmt.Errorf(
		"claude.settings.%s must be a positive whole number of days, got %v",
		knobCleanupPeriodDays,
		value,
	)
}

// cloneOwnerSettings is a deep copy of settings, so a launch merging into it never writes into
// the persona it came from.
func cloneOwnerSettings(settings map[string]any) (map[string]any, error) {
	if len(settings) == 0 {
		return nil, nil
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		return nil, fmt.Errorf("copy the project's claude.settings: %w", err)
	}
	var clone map[string]any
	if err := json.Unmarshal(encoded, &clone); err != nil {
		return nil, fmt.Errorf("copy the project's claude.settings: %w", err)
	}
	return clone, nil
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
			mergeEnv(theirs, mine)
		case key == settingsEnvKey && ours:
			if pfmEnv, typed := settings[key].(map[string]string); typed {
				merged := make(map[string]any, len(pfmEnv)+len(mine))
				for name, variable := range pfmEnv {
					merged[name] = variable
				}
				mergeEnv(merged, mine)
				settings[key] = merged
				continue
			}
			settings[key] = value
		default:
			settings[key] = value
		}
	}
}

// mergeEnv lays layer's variables over env, a null included: it outranks an earlier layer's value
// until dropNullEnv removes the variable on the wire, the one way a layer turns off a variable an
// earlier one set (Claude Code reads any value of a flag variable as on).
func mergeEnv(env, layer map[string]any) {
	for name, variable := range layer {
		env[name] = variable
	}
}

// dropNullEnv removes the variables settings' env sets to null, once every layer is merged.
func dropNullEnv(settings map[string]any) {
	if env, ok := settings[settingsEnvKey].(map[string]any); ok {
		for name, variable := range env {
			if variable == nil {
				delete(env, name)
			}
		}
	}
}
