package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
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

// A test must refuse the current checkout if a marker would point there.
func TestRefuseAmbientConfigHomeFromRefusesCurrentClone(t *testing.T) {
	home := t.TempDir()
	repo, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := paths.WriteSourceRepoMarker(home, repo); err != nil {
		t.Fatal(err)
	}
	env := &paths.MapEnv{Values: map[string]string{}}
	if err := RefuseAmbientConfigHomeFrom(env, home); err == nil || !strings.Contains(err.Error(), paths.EnvConfig) {
		t.Fatalf("current-clone marker error = %v, want a jailed override remedy", err)
	}
	env.Values[paths.EnvConfig] = filepath.Join(home, "pfm.config.json")
	if err := RefuseAmbientConfigHomeFrom(env, home); err != nil {
		t.Fatalf("jailed override refused: %v", err)
	}
}

func TestRefuseAmbientConfigHomeFromAllowsNoMarkerAndRealHomeOptOut(t *testing.T) {
	home := t.TempDir()
	env := &paths.MapEnv{Values: map[string]string{}}
	if err := RefuseAmbientConfigHomeFrom(env, home); err != nil {
		t.Fatal(err)
	}
	repo, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := paths.WriteSourceRepoMarker(home, repo); err != nil {
		t.Fatal(err)
	}
	env.Values[paths.EnvRealHome] = "1"
	if err := RefuseAmbientConfigHomeFrom(env, home); err != nil {
		t.Fatal(err)
	}
}

func TestRefuseAmbientConfigHomeFromAlias(t *testing.T) {
	home := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	physical, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Dir(physical)
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(parent, alias); err != nil {
		t.Fatal(err)
	}
	if err := paths.WriteSourceRepoMarker(home, filepath.Join(alias, filepath.Base(physical))); err != nil {
		t.Fatal(err)
	}
	if err := RefuseAmbientConfigHomeFrom(&paths.MapEnv{Values: map[string]string{}}, home); err == nil {
		t.Fatal("alias marker allowed ambient config")
	}
}
