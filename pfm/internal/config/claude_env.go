package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
)

// claudeEnvNamePattern is a claude.env name: upper snake, starting with a
// letter, so it is a portable environment variable name.
var claudeEnvNamePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// claudeEnvRefused names plugin variables owned by launch or pfm's process.
// claudelaunch imports config, so its registry parity is checked from that
// package by TestClaudeEnvRefusesLaunchNames rather than an import cycle here.
var claudeEnvRefused = map[string]string{
	"CACHE_LIVE_CONTROL_MAIN_TTL":   "is set on every launch from claude.cache1h: set that key instead",
	"CACHE_LIVE_CONTROL_AGENTS_TTL": "is discarded on every launch as inherited state: set CACHE_LIVE_CONTROL_SUBAGENT_TTL instead",
	"CALLMETER_HOME":                "is read by pfm itself, which builds callmeter's binary under it: export it where pfm runs instead",
	"ENGINE_SYNC_HOME":              "is read by pfm itself, which runs engine-sync from it: export it where pfm runs instead",
}

// claudeEnvSet reports whether raw carries claude.env: absent and null are unset.
func claudeEnvSet(raw json.RawMessage) bool {
	return len(raw) != 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// decodeClaudeEnv decodes claude.env: an object whose names are upper snake
// and whose values are strings. Launch-owned plugin names are refused.
func decodeClaudeEnv(raw json.RawMessage) (map[string]string, error) {
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf(
			"%s must be an object of variable names to string values, got %s", KeyClaudeEnv, bytes.TrimSpace(raw),
		)
	}
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	env := make(map[string]string, len(entries))
	for _, name := range names {
		if !claudeEnvNamePattern.MatchString(name) {
			return nil, fmt.Errorf(
				"%s name %q must be upper snake: A-Z, 0-9 and _, starting with a letter", KeyClaudeEnv, name,
			)
		}
		if reason, refused := claudeEnvRefused[name]; refused {
			return nil, fmt.Errorf("%s.%s %s", KeyClaudeEnv, name, reason)
		}
		var value *string
		if err := json.Unmarshal(entries[name], &value); err != nil || value == nil {
			return nil, fmt.Errorf("%s.%s must be a string, got %s", KeyClaudeEnv, name, entries[name])
		}
		env[name] = *value
	}
	return env, nil
}
