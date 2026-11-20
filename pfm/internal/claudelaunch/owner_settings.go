package claudelaunch

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"os"
	"strings"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
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
	rest, values := splitSettingsArgs(args)
	for _, value := range values {
		owner, readErr := ownerSettings(value)
		if readErr != nil {
			return nil, "", readErr
		}
		mergeOwnerSettings(settings, owner)
	}
	dropNullSettings(settings)
	encoded, err := json.Marshal(settings)
	if err != nil {
		return nil, "", fmt.Errorf("render --settings: %w", err)
	}
	return rest, string(encoded), nil
}

// splitSettingsArgs separates the caller's words from the values of the
// --settings flags among them (`--settings v` and `--settings=v`), in order;
// everything from a "--" on is the caller's.
func splitSettingsArgs(args []string) (rest, values []string) {
	rest = make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		word := args[index]
		if word == "--" {
			return append(rest, args[index:]...), values
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
		values = append(values, value)
	}
	return rest, values
}

// withCallerTUI is layered with the "tui" a caller's own --settings sets laid
// over it, the last one winning as in settingsArgv, so the fullscreen verdict
// judges the tui the launch carries. A --settings value that cannot be read
// is passed over here: settingsArgv refuses the launch over it next.
func withCallerTUI(args []string, layered map[string]any) map[string]any {
	_, values := splitSettingsArgs(args)
	var tui any
	found := false
	for _, value := range values {
		if owner, err := ownerSettings(value); err == nil {
			if set, present := owner[settingsTUI]; present {
				tui, found = set, true
			}
		}
	}
	if !found {
		return layered
	}
	view := maps.Clone(layered)
	if view == nil {
		view = map[string]any{}
	}
	view[settingsTUI] = tui
	return view
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
// until dropNullSettings removes the variable on the wire, the one way a layer turns off a variable an
// earlier one set (Claude Code reads any value of a flag variable as on).
func mergeEnv(env, layer map[string]any) {
	for name, variable := range layer {
		env[name] = variable
	}
}

// dropNullSettings removes the top-level keys and the env variables settings sets to null, once every
// layer is merged: a null turns off what an earlier layer or the shipped defaults set, so the launch
// carries no value of its own there and Claude reads the account's settings.json.
func dropNullSettings(settings map[string]any) {
	for key, value := range settings {
		if value == nil {
			delete(settings, key)
		}
	}
	if env, ok := settings[settingsEnvKey].(map[string]any); ok {
		for name, variable := range env {
			if variable == nil {
				delete(env, name)
			}
		}
	}
}
