package config

import (
	"path/filepath"
	"testing"

	"hostops/pfm/internal/paths"
)

// TestAmbientClaudeConfigDirCleansOrReportsUnset pins the one rule the
// launcher shim and the Claude registry resolver both read through: unset or
// blank is "", and a set value comes back Cleaned so a trailing slash or a
// "./" segment never makes an otherwise-identical directory compare unequal
// to the physical path a registry write resolves.
func TestAmbientClaudeConfigDirCleansOrReportsUnset(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if got := AmbientClaudeConfigDir(); got != "" {
		t.Fatalf("AmbientClaudeConfigDir()=%q, want empty when unset", got)
	}

	t.Setenv("CLAUDE_CONFIG_DIR", "  ")
	if got := AmbientClaudeConfigDir(); got != "" {
		t.Fatalf("AmbientClaudeConfigDir()=%q, want empty when blank", got)
	}

	want := filepath.Join(t.TempDir(), ".cc", "2")
	t.Setenv("CLAUDE_CONFIG_DIR", want+string(filepath.Separator))
	if got := AmbientClaudeConfigDir(); got != want {
		t.Fatalf("AmbientClaudeConfigDir()=%q, want cleaned %q", got, want)
	}
}

// TestRefuseAmbientConfigHomeFromAllowsTheJailsOwnPin pins the L3-F9 write
// half's non-firing case: internal/testjail's jailHome/Fleet/CleanHome all
// pin XDG_CONFIG_HOME to exactly home's own .config subdirectory, so that
// pin must never be refused.
func TestRefuseAmbientConfigHomeFromAllowsTheJailsOwnPin(t *testing.T) {
	home := "/jailed/home"
	env := &paths.MapEnv{Values: map[string]string{"XDG_CONFIG_HOME": filepath.Join(home, ".config")}}
	if err := RefuseAmbientConfigHomeFrom(env, home); err != nil {
		t.Fatalf("RefuseAmbientConfigHomeFrom() = %v, want nil for the jail's own pin", err)
	}
}

// TestRefuseAmbientConfigHomeFromAllowsUnsetOrRelativeXDG pins the case
// ConfigHomeFrom itself already falls back to home for: nothing to refuse
// when XDG_CONFIG_HOME carries no absolute override at all.
func TestRefuseAmbientConfigHomeFromAllowsUnsetOrRelativeXDG(t *testing.T) {
	home := "/jailed/home"
	for _, xdg := range []string{"", "relative"} {
		env := &paths.MapEnv{Values: map[string]string{"XDG_CONFIG_HOME": xdg}}
		if err := RefuseAmbientConfigHomeFrom(env, home); err != nil {
			t.Fatalf("RefuseAmbientConfigHomeFrom() with XDG=%q = %v, want nil", xdg, err)
		}
	}
}

// TestRefuseAmbientConfigHomeFromRefusesAnAmbientLeak pins L3-F9's write
// half: an absolute XDG_CONFIG_HOME that does not derive from the jailed
// home is exactly the operator's real config reaching a jailed test, and
// must be refused with a named error, never silently accepted.
func TestRefuseAmbientConfigHomeFromRefusesAnAmbientLeak(t *testing.T) {
	home := "/jailed/home"
	env := &paths.MapEnv{Values: map[string]string{"XDG_CONFIG_HOME": "/operators/real/config"}}
	err := RefuseAmbientConfigHomeFrom(env, home)
	if err == nil {
		t.Fatal("RefuseAmbientConfigHomeFrom() accepted an XDG_CONFIG_HOME outside the jailed home")
	}
}

// TestRefuseAmbientConfigHomeFromOptsOutWithRealHome pins the same escape
// hatch HomeFrom already offers: PFM_TEST_REAL_HOME=1 lets a test that
// genuinely must read the host's real config opt back in.
func TestRefuseAmbientConfigHomeFromOptsOutWithRealHome(t *testing.T) {
	home := "/jailed/home"
	env := &paths.MapEnv{Values: map[string]string{
		"XDG_CONFIG_HOME": "/operators/real/config",
		paths.EnvRealHome: "1",
	}}
	if err := RefuseAmbientConfigHomeFrom(env, home); err != nil {
		t.Fatalf("RefuseAmbientConfigHomeFrom() with %s=1 = %v, want nil", paths.EnvRealHome, err)
	}
}
