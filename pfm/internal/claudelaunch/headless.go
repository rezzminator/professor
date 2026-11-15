package claudelaunch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode"

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
	knobWebSearchesPerSession: kindInt, knobAutoCompactWindow: kindInt, knobHelperModel: kindString,
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
	if key == knobHelperModel {
		if text, ok := value.(string); ok && validHelperModel(text) {
			return nil
		}
		return fmt.Errorf("%s: expected a non-empty model name without whitespace", key)
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
	result, env := defaultSettings()
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
			setFlagEnv(env, envNativeCursor, value.(bool))
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
		case knobHelperModel:
			env[envAnthropicSmallFastModel] = value.(string)
		case knobTmuxTruecolor:
			setFlagEnv(env, envTmuxTruecolor, value.(bool))
		case knobTheme:
			if theme, _ := themeForLaunch(value.(string)); theme != "" {
				result[knobTheme] = theme
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

// setFlagEnv sets name to "1" when on, else removes it, a default's value included: Claude Code reads
// any non-empty value of these as on.
func setFlagEnv(env map[string]string, name string, on bool) {
	if on {
		env[name] = "1"
		return
	}
	delete(env, name)
}

// validHelperModel reports whether value can be a helper model: a model id or alias, non-empty,
// without a space or control character, so it reaches Claude Code's environment as one word.
func validHelperModel(value string) bool {
	return value != "" && !strings.ContainsFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	})
}
