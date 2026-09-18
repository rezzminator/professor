package config

import (
	"path/filepath"
	"strings"

	"hostops/pfm/internal/paths"
)

// AmbientClaudeConfigDir returns the CLAUDE_CONFIG_DIR the invoking shell has
// exported, cleaned, or "" when unset or blank. The launcher shim
// (cmd/pfm/internal_launch.go) and the Claude user-registry resolver
// (installer.ClaudeUserRegistries) both read this SAME env var through this
// one helper, by the same rule, so the two doors cannot drift out of sync.
func AmbientClaudeConfigDir() string {
	return AmbientClaudeConfigDirFrom(paths.OSEnv{})
}

// AmbientClaudeConfigDirFrom applies the same rule over an injected environment.
func AmbientClaudeConfigDirFrom(env paths.Env) string {
	value := strings.TrimSpace(env.Get("CLAUDE_CONFIG_DIR"))
	if value == "" {
		return ""
	}
	return filepath.Clean(value)
}

// ResolvePath applies pfm's XDG rule: only an absolute XDG_CONFIG_HOME wins.
func ResolvePath(home string) string { return ResolvePathFrom(paths.OSEnv{}, home) }

// ResolvePathFrom applies pfm's XDG rule over an injected environment.
func ResolvePathFrom(env paths.Env, home string) string {
	root := env.Get("XDG_CONFIG_HOME")
	if !filepath.IsAbs(root) {
		root = filepath.Join(home, ".config")
	}
	return filepath.Join(filepath.Clean(root), "pfm", FileName)
}
