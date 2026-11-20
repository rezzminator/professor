package config

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// The keys a file sets that its scope never reads: the retired claude launch keys, accepted as no-ops
// at the machine scope, and every key but the scope keys at the project scope.

// retiredClaudeKeys are the claude keys whose values moved into the shipped launch defaults, each with
// the claude.settings path that sets it now. The loader accepts each as a no-op.
var retiredClaudeKeys = map[string]string{
	"webSearchesPerSession":  "claude.settings.env.CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION",
	"autoCompactWindow":      "claude.settings.env.CLAUDE_CODE_AUTO_COMPACT_WINDOW",
	"helperModel":            "claude.settings.env.ANTHROPIC_SMALL_FAST_MODEL",
	"tmuxTruecolor":          "claude.settings.env.CLAUDE_CODE_TMUX_TRUECOLOR" + " (null turns it off)",
	"nativeCursor":           "claude.settings.env.CLAUDE_CODE_NATIVE_CURSOR" + " (null turns it off)",
	"maxSubagentSpawnDepth":  "claude.settings.env.CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH",
	"maxConcurrentSubagents": "claude.settings.env.CLAUDE_CODE_MAX_CONCURRENT_SUBAGENTS",
	"cleanupPeriodDays":      "claude.settings.cleanupPeriodDays",
}

// projectOnlyWarnings names each key content sets that a project scope does not read.
func projectOnlyWarnings(content []byte, path string) []string {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(content, &top); err != nil {
		return nil // decodeVersioned already decoded content
	}
	var keys []string
	for key, value := range top {
		switch key {
		case KeyEnvs, KeyHarnessPrompt:
		case claudeKey:
			var claude map[string]json.RawMessage
			if json.Unmarshal(value, &claude) != nil {
				keys = append(keys, key)
				continue
			}
			for name := range claude {
				if name != keySettings {
					keys = append(keys, claudePrefix+name)
				}
			}
		default:
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	warnings := make([]string, 0, len(keys))
	for _, key := range keys {
		if instead, retired := retiredClaudeKeys[strings.TrimPrefix(key, claudePrefix)]; retired &&
			strings.HasPrefix(key, claudePrefix) {
			warnings = append(warnings, fmt.Sprintf("%s is retired: set %s instead; ignored in %s", key, instead, path))
			continue
		}
		warnings = append(warnings, fmt.Sprintf("%s is machine-only; ignored in %s", key, path))
	}
	return warnings
}

// claudePrefix opens every key of the claude block ("claude."), claudeKey names the block itself.
var (
	claudePrefix = strings.TrimSuffix(KeyClaudeSettings, keySettings)
	claudeKey    = strings.TrimSuffix(claudePrefix, ".")
)

// retiredKeys are the retired keys raw sets, at the top claude block and each account's, sorted.
func retiredKeys(raw rawConfig) []string {
	var keys []string
	collect := func(prefix string, claude *rawClaude) {
		if claude == nil {
			return
		}
		for name, value := range claude.retired() {
			if len(value) != 0 {
				keys = append(keys, prefix+name)
			}
		}
	}
	collect(claudePrefix, raw.Claude)
	if raw.Accounts != nil {
		for index, account := range *raw.Accounts {
			collect(fmt.Sprintf("accounts[%d].%s.", index, claudeKey), account.Claude)
		}
	}
	slices.Sort(keys)
	return keys
}

// RetiredKeyWarnings names each retired key the machine file sets and the claude.settings path
// that sets its value now.
func (config Config) RetiredKeyWarnings() []string {
	warnings := make([]string, 0, len(config.Retired))
	for _, key := range config.Retired {
		name := key[strings.LastIndex(key, ".")+1:]
		warnings = append(warnings, fmt.Sprintf("%s is retired: set %s instead", key, retiredClaudeKeys[name]))
	}
	return warnings
}

// retired is each retired key of the claude block by name, with its raw value (empty when unset).
func (claude rawClaude) retired() map[string]json.RawMessage {
	return map[string]json.RawMessage{
		"webSearchesPerSession":  claude.RetiredWebSearchesPerSession,
		"autoCompactWindow":      claude.RetiredAutoCompactWindow,
		"helperModel":            claude.RetiredHelperModel,
		"tmuxTruecolor":          claude.RetiredTmuxTruecolor,
		"cleanupPeriodDays":      claude.RetiredCleanupPeriodDays,
		"nativeCursor":           claude.RetiredNativeCursor,
		"maxSubagentSpawnDepth":  claude.RetiredMaxSubagentSpawnDepth,
		"maxConcurrentSubagents": claude.RetiredMaxConcurrentSubagents,
	}
}
