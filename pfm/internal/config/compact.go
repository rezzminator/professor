package config

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// Claude Code compacts the main chat and its sub-agents at one window: a
// sub-agent inherits its parent's threshold and the harness has no setting of
// its own for it. Two token counts under the top-level claude block name the
// point each party may compact at; the gate that enforces them reads them from
// here. They are machine-wide — the launch window is one per host — so an
// account block that names either is refused rather than silently ignored.
const (
	// MinCompactTokens and MaxCompactTokens bound a threshold, inclusive.
	MinCompactTokens = 100000
	MaxCompactTokens = 1000000

	autoCompactMainKey     = "autoCompactMain"
	autoCompactSubagentKey = "autoCompactSubagent"
)

// ParseCompactTokens reads one threshold: a plain integer (150000) or a number
// with a k/K (thousands) or m/M (millions) suffix, a decimal allowed only with
// a suffix and only when it lands on a whole token count (150.5k, 0.5m). The
// result is range-checked against MinCompactTokens..MaxCompactTokens.
func ParseCompactTokens(raw string) (int, error) {
	text := strings.TrimSpace(raw)
	multiplier := int64(1)
	switch {
	case strings.HasSuffix(text, "k"), strings.HasSuffix(text, "K"):
		multiplier, text = 1000, text[:len(text)-1]
	case strings.HasSuffix(text, "m"), strings.HasSuffix(text, "M"):
		multiplier, text = 1000000, text[:len(text)-1]
	}
	whole, fraction, decimal := strings.Cut(text, ".")
	// Digits only on both sides of the point: no sign, no exponent, no space.
	digits := func(part string) bool { return part != "" && strings.Trim(part, "0123456789") == "" }
	if !digits(whole) || (decimal && (multiplier == 1 || !digits(fraction) || len(fraction) > 6)) {
		return 0, fmt.Errorf(
			"%q is not a token count (an integer, or a number with k or m: 150000, 150k, 1m)",
			raw,
		)
	}
	// More digits than the ceiling has is out of range, and never overflows.
	if len(strings.TrimLeft(whole, "0")) > len(strconv.Itoa(MaxCompactTokens)) {
		return 0, compactRangeError(raw)
	}
	value, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a token count: %w", raw, err)
	}
	value *= multiplier
	if decimal {
		scale := int64(1)
		for range fraction {
			scale *= 10
		}
		part, err := strconv.ParseInt(fraction, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("%q is not a token count: %w", raw, err)
		}
		if part*multiplier%scale != 0 {
			return 0, fmt.Errorf("%q is not a whole number of tokens", raw)
		}
		value += part * multiplier / scale
	}
	if value < MinCompactTokens || value > MaxCompactTokens {
		return 0, compactRangeError(raw)
	}
	return int(value), nil
}

func compactRangeError(raw string) error {
	return fmt.Errorf("%q is outside %d..%d tokens", raw, MinCompactTokens, MaxCompactTokens)
}

// CompactThresholds is the pair the gate enforces; ok only when both are set,
// since one threshold alone cannot tell the main chat's compaction from a
// sub-agent's.
func (prefs ClaudePrefs) CompactThresholds() (main, sub int, ok bool) {
	return prefs.AutoCompactMain, prefs.AutoCompactSubagent, prefs.AutoCompactMain > 0 && prefs.AutoCompactSubagent > 0
}

// decodeCompactThresholds validates both keys at data entry for one claude
// block: the top level (index -1) takes them, an account block refuses them.
func decodeCompactThresholds(prefs *ClaudePrefs, raw rawClaude, path, scope string, index int) error {
	for _, field := range []struct {
		key    string
		value  json.RawMessage
		target *int
	}{
		{autoCompactMainKey, raw.AutoCompactMain, &prefs.AutoCompactMain},
		{autoCompactSubagentKey, raw.AutoCompactSubagent, &prefs.AutoCompactSubagent},
	} {
		if field.value == nil {
			continue
		}
		if index >= 0 {
			return fmt.Errorf(
				"config %s: %s.%s is machine-wide; set it in the top-level claude block",
				path,
				configScope(scope, index),
				field.key,
			)
		}
		tokens, err := compactTokensValue(field.value)
		if err != nil {
			return fmt.Errorf("config %s: %s.%s: %w", path, configScope(scope, index), field.key, err)
		}
		*field.target = tokens
	}
	return nil
}

// compactTokensValue accepts a JSON string or a JSON number; the number's
// literal text goes through the same parser, so 150000.5 is refused like
// "150000.5" rather than truncated.
func compactTokensValue(value json.RawMessage) (int, error) {
	text := strings.TrimSpace(string(value))
	if strings.HasPrefix(text, `"`) {
		if err := json.Unmarshal(value, &text); err != nil {
			return 0, fmt.Errorf("decode %s: %w", value, err)
		}
	}
	return ParseCompactTokens(text)
}

// applyCompactThresholds copies the top-level thresholds decodeClaudePrefs
// validated onto the resolved config and records each set key's source.
func applyCompactThresholds(target *ClaudePrefs, decoded ClaudePrefs, raw rawClaude, sources map[string]Source) {
	if raw.AutoCompactMain != nil {
		target.AutoCompactMain = decoded.AutoCompactMain
		sources[engineConfigKey(pfmengine.Claude, autoCompactMainKey)] = SourceFile
	}
	if raw.AutoCompactSubagent != nil {
		target.AutoCompactSubagent = decoded.AutoCompactSubagent
		sources[engineConfigKey(pfmengine.Claude, autoCompactSubagentKey)] = SourceFile
	}
}
