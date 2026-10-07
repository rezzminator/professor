package claudelaunch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

const (
	kindBool   = "bool"
	kindInt    = "int"
	kindString = "string"
)

var headlessKeys = map[string]string{
	knobCache1H: kindBool, knobSystemPrompt: kindString, knobNativeCursor: kindBool,
	knobMaxSubagentSpawnDepth: kindInt, knobMaxConcurrentSubagents: kindInt,
	knobWebSearchesPerSession: kindInt, knobAutoCompactWindow: kindInt,
	knobTmuxTruecolor: kindBool, knobTheme: kindString,
	knobCleanupPeriodDays: kindInt,
}

// ParseSettings decodes one Claude preferences object from a --pfm-settings file.
func ParseSettings(raw []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var input map[string]any
	if err := decoder.Decode(&input); err != nil {
		return nil, fmt.Errorf("pfm-settings: %w", err)
	}
	if input == nil {
		return nil, fmt.Errorf("pfm-settings: expected object")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("pfm-settings: trailing data")
		}
		return nil, fmt.Errorf("pfm-settings: trailing data: %w", err)
	}
	if wrapped, ok := input[pfmengine.MustLookup(pfmengine.Claude).LongName]; ok && len(input) == 1 {
		block, ok := wrapped.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("claude: expected object")
		}
		input = block
	}
	for key, value := range input {
		kind, ok := headlessKeys[key]
		if !ok && key != knobPermissionMode && key != knobBinary {
			return nil, fmt.Errorf("unknown key %s", key)
		}
		if key == knobPermissionMode || key == knobBinary {
			kind = kindString
		}
		if err := checkValue(key, kind, value); err != nil {
			return nil, err
		}
	}
	return input, nil
}

func checkValue(key, kind string, value any) error {
	if key == knobAutoCompactWindow {
		if number, ok := integer(value); ok && number > 0 {
			return nil
		}
		return fmt.Errorf("%s: expected positive int", key)
	}
	switch kind {
	case kindBool:
		if _, ok := value.(bool); ok {
			return nil
		}
	case kindString:
		if _, ok := value.(string); ok {
			return nil
		}
	case kindInt:
		if _, ok := integer(value); ok {
			return nil
		}
	}
	return fmt.Errorf("%s: expected %s", key, kind)
}

func integer(value any) (int64, bool) {
	switch number := value.(type) {
	case int:
		return int64(number), true
	case int64:
		return number, true
	case json.Number:
		value, err := number.Int64()
		return value, err == nil
	case float64:
		if math.Trunc(number) == number && number >= math.MinInt64 && number <= math.MaxInt64 {
			return int64(number), true
		}
	}
	return 0, false
}

// RenderHeadless turns supported per-run Claude preferences into --settings JSON.
func RenderHeadless(settings map[string]any) (string, error) {
	result := map[string]any{knobOutputStyle: defaultWord}
	env := map[string]string{envFunctionHooks: "1"}
	for _, knob := range Knobs {
		if knob.Name == knobAutoCompactWindow {
			env[envAutoCompactWindow] = strconv.FormatInt(knob.Default.(int64), 10)
			break
		}
	}
	for key, value := range settings {
		kind, ok := headlessKeys[key]
		if !ok {
			return "", fmt.Errorf("unsupported key %s", key)
		}
		if err := checkValue(key, kind, value); err != nil {
			return "", err
		}
		switch key {
		case knobCache1H:
			env[envCacheLiveControlMainTTL] = promptCacheTTL(value.(bool))
		case knobSystemPrompt:
			switch value.(string) {
			case productionMode:
			case "lean":
				env[envSimplePrompt] = "1"
			default:
				return "", fmt.Errorf("systemPrompt: only lean or production is supported")
			}
		case knobNativeCursor:
			if value.(bool) {
				env[envNativeCursor] = "1"
			}
		case knobMaxSubagentSpawnDepth:
			env[envSpawnDepth] = strconv.FormatInt(mustInteger(value), 10)
		case knobMaxConcurrentSubagents:
			if n := mustInteger(value); n > 0 {
				env[envConcurrentSubagents] = strconv.FormatInt(n, 10)
			}
		case knobWebSearchesPerSession:
			env[envWebSearches] = strconv.FormatInt(mustInteger(value), 10)
		case knobAutoCompactWindow:
			env[envAutoCompactWindow] = strconv.FormatInt(mustInteger(value), 10)
		case knobTmuxTruecolor:
			if value.(bool) {
				env[envTmuxTruecolor] = "1"
			}
		case knobTheme:
			if value.(string) != "" {
				result[knobTheme] = value
			}
		case knobCleanupPeriodDays:
			result[knobCleanupPeriodDays] = mustInteger(value)
		}
	}
	if len(env) != 0 {
		result["env"] = env
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("render headless settings: %w", err)
	}
	return string(encoded), nil
}

func mustInteger(value any) int64 { number, _ := integer(value); return number }
