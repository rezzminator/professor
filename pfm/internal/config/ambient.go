package config

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

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

// ResolvePath applies pfm's XDG rule: only an absolute XDG_CONFIG_HOME wins,
// through paths.ConfigHomeFrom — the single place that rule is computed, so
// this and internal/testjail's own jail pin can never drift about which
// .config a caller meant.
func ResolvePath(home string) string { return ResolvePathFrom(paths.OSEnv{}, home) }

// ResolvePathFrom applies pfm's XDG rule over an injected environment.
func ResolvePathFrom(env paths.Env, home string) string {
	return filepath.Join(paths.ConfigHomeFrom(env, home), "pfm", FileName)
}

// RefuseAmbientConfigHome is LoadRuntime and LoadDiagnosticRuntime's guard
// against L3-F9's other half: a jailed test's PFM_HOME says nothing about
// XDG_CONFIG_HOME, so an operator's own absolute XDG_CONFIG_HOME reaches
// config.LoadRuntime("") regardless of how jailed home is, and resolves the
// operator's REAL pfm/config.* — the accounts, MCP servers, theme a live
// pfm reads.
//
// A properly jailed test rehomes XDG_CONFIG_HOME alongside home:
// internal/testjail's jailHome, Fleet/FleetEnv and CleanHome all pin it to
// exactly paths.ConfigHomeFrom's own HOME-derived fallback (home's
// ".config" subdirectory), so comparing the two tells a jail's own
// correctly re-homed XDG_CONFIG_HOME apart from an ambient leak without
// reading any OS account record — no new host door, and no env-only
// ambiguity, because a properly jailed test's XDG_CONFIG_HOME is not merely
// "under home", it is byte-identical to the fallback home alone would have
// produced.
func RefuseAmbientConfigHome(home string) error {
	return RefuseAmbientConfigHomeFrom(paths.OSEnv{}, home)
}

// RefuseAmbientConfigHomeFrom is RefuseAmbientConfigHome over an injected
// environment, following paths.HomeFrom's own testing.Testing() shape:
// refuse only inside a test, and PFM_TEST_REAL_HOME=1 opts back in the rare
// test that genuinely must read the host's real config.
func RefuseAmbientConfigHomeFrom(env paths.Env, home string) error {
	if !testing.Testing() || env.Get(paths.EnvRealHome) != "" {
		return nil
	}
	root := env.Get("XDG_CONFIG_HOME")
	if !filepath.IsAbs(root) {
		return nil
	}
	if filepath.Clean(root) == filepath.Clean(filepath.Join(home, ".config")) {
		return nil
	}
	// The package jail's own pin: a test that moved PFM_HOME to a directory of
	// its own still carries it, and it is a jailed path, never the operator's.
	if jail := env.Get(paths.EnvTestJailHome); filepath.IsAbs(jail) &&
		filepath.Clean(root) == filepath.Clean(filepath.Join(jail, ".config")) {
		return nil
	}
	return fmt.Errorf(
		"refusing ambient XDG_CONFIG_HOME %s inside a test: it does not derive from the jailed home %s "+
			"(see internal/testjail), or set %s=1 if this test genuinely must read the host",
		filepath.Clean(root), home, paths.EnvRealHome,
	)
}
