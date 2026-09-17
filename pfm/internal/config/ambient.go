package config

import (
	"os"
	"path/filepath"
	"strings"
)

// AmbientClaudeConfigDir returns the CLAUDE_CONFIG_DIR the invoking shell has
// exported, cleaned, or "" when unset or blank. The launcher shim
// (cmd/pfm/internal_launch.go) and the Claude user-registry resolver
// (installer.ClaudeUserRegistries) both read this SAME env var through this
// one helper, by the same rule, so the two doors cannot drift out of sync.
func AmbientClaudeConfigDir() string {
	value := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
	if value == "" {
		return ""
	}
	return filepath.Clean(value)
}
