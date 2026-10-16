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

// claudeEnvSet reports whether raw carries claude.env: absent and null are unset.
func claudeEnvSet(raw json.RawMessage) bool {
	return len(raw) != 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// decodeClaudeEnv decodes claude.env: an object whose names are upper snake
// and whose values are strings.
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
		var value *string
		if err := json.Unmarshal(entries[name], &value); err != nil || value == nil {
			return nil, fmt.Errorf("%s.%s must be a string, got %s", KeyClaudeEnv, name, entries[name])
		}
		env[name] = *value
	}
	return env, nil
}
