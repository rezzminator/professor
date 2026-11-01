package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/resolve"
)

// clearCallerEngineEnv isolates a test's empty-engine resolution from this
// shell's own ambient session env: resolveRunEngineAccount now consults
// the caller environment (resolve.CallerEnvironmentFrom) before falling to the config default, so a
// developer running `go test` from inside a live Claude Code or Codex chat
// would otherwise pick up CLAUDE_CODE_SESSION_ID/CODEX_THREAD_ID and route
// away from the config default these tests pin (pfm/CLAUDE.md § Testing
// Rules: a test depending on the developer's environment is an isolation
// defect).
func clearCallerEngineEnv(t *testing.T) {
	t.Helper()
	t.Setenv(resolve.ClaudeSessionEnv, "")
	t.Setenv(resolve.CodexThreadEnv, "")
}

func TestResolveRunEngineAccountUsesTheChosenRoster(t *testing.T) {
	clearCallerEngineEnv(t)
	machine := pfmconfig.Config{
		Version:       pfmconfig.Version,
		CodexAccounts: []pfmconfig.CodexAccount{{ID: 3, Home: "/codex/3"}, {ID: 8, Home: "/codex/8"}},
		Ask:           pfmconfig.AskConfig{Engine: pfmengine.Codex},
	}

	engine, account, err := resolveRunEngineAccount("", 0, machine, 0)
	if err != nil || engine != "cx" || account != 3 {
		t.Fatalf("default = %q/%d error=%v, want cx/3", engine, account, err)
	}
	engine, account, err = resolveRunEngineAccount("codex", 8, machine, 0)
	if err != nil || engine != "cx" || account != 8 {
		t.Fatalf("explicit = %q/%d error=%v, want cx/8", engine, account, err)
	}
	_, _, err = resolveRunEngineAccount("claude", 1, machine, 0)
	if err == nil || !strings.Contains(err.Error(), "requested Claude account 1") {
		t.Fatalf("empty Claude roster error = %v", err)
	}
}

func TestResolveRunEngineAccountValidatesOpenCodeRoster(t *testing.T) {
	clearCallerEngineEnv(t)
	machine := pfmconfig.Config{
		OpenCodeAccounts: []pfmconfig.OpenCodeAccount{{ID: 5, Home: "/opencode"}},
		Ask:              pfmconfig.AskConfig{Engine: pfmengine.OpenCode},
	}
	engine, account, err := resolveRunEngineAccount("", 0, machine, 0)
	if err != nil || engine != pfmengine.OpenCode || account != 5 {
		t.Fatalf("default OpenCode = %q/%d error=%v, want ox/5", engine, account, err)
	}
	_, _, err = resolveRunEngineAccount("opencode", 8, machine, 0)
	if err == nil || !strings.Contains(err.Error(), "OpenCode account 8") {
		t.Fatalf("invalid OpenCode account error = %v", err)
	}
}

// TestResolveRunEngineAccountDefaultsToCallerEngine pins that an empty
// --engine consults the CALLING chat's own ambient session env before
// falling back to the machine's configured default engine, and that an
// explicit --engine always wins over caller env regardless.
func TestResolveRunEngineAccountDefaultsToCallerEngine(t *testing.T) {
	machine := pfmconfig.Config{
		Version:       pfmconfig.Version,
		Accounts:      []pfmconfig.Account{{ID: 1, ConfigDir: "/claude/1"}},
		CodexAccounts: []pfmconfig.CodexAccount{{ID: 3, Home: "/codex/3"}},
		Ask:           pfmconfig.AskConfig{Engine: pfmengine.Codex},
	}

	t.Run("claude caller env wins over a codex-default machine", func(t *testing.T) {
		t.Setenv(resolve.ClaudeSessionEnv, "caller-claude-sess")
		t.Setenv(resolve.CodexThreadEnv, "")
		engine, account, err := resolveRunEngineAccount("", 0, machine, 1)
		if err != nil || engine != pfmengine.Claude || account != 1 {
			t.Fatalf("resolveRunEngineAccount = %q/%d error=%v, want cc/1", engine, account, err)
		}
	})

	t.Run("codex caller env used when claude env is absent", func(t *testing.T) {
		t.Setenv(resolve.ClaudeSessionEnv, "")
		t.Setenv(resolve.CodexThreadEnv, "caller-codex-thread")
		engine, account, err := resolveRunEngineAccount("", 0, machine, 1)
		if err != nil || engine != pfmengine.Codex || account != 3 {
			t.Fatalf("resolveRunEngineAccount = %q/%d error=%v, want cx/3", engine, account, err)
		}
	})

	t.Run("no caller env falls back to the machine's configured default", func(t *testing.T) {
		t.Setenv(resolve.ClaudeSessionEnv, "")
		t.Setenv(resolve.CodexThreadEnv, "")
		engine, account, err := resolveRunEngineAccount("", 0, machine, 1)
		if err != nil || engine != pfmengine.Codex || account != 3 {
			t.Fatalf("resolveRunEngineAccount = %q/%d error=%v, want cx/3 (config default)", engine, account, err)
		}
	})

	t.Run("explicit --engine overrides a present claude caller env", func(t *testing.T) {
		t.Setenv(resolve.ClaudeSessionEnv, "caller-claude-sess")
		t.Setenv(resolve.CodexThreadEnv, "")
		engine, account, err := resolveRunEngineAccount("cx", 0, machine, 1)
		if err != nil || engine != pfmengine.Codex || account != 3 {
			t.Fatalf("resolveRunEngineAccount = %q/%d error=%v, want cx/3 (explicit wins)", engine, account, err)
		}
	})
}

func TestPrimaryAccountOrImplicitStandsInWhenThePrimaryCannotBeRead(t *testing.T) {
	home := t.TempDir()
	values := paths.Values{Home: home, StateDB: filepath.Join(home, "absent.db")}
	machine := pfmconfig.Config{Accounts: []pfmconfig.Account{{ID: 3}, {ID: 5}}}
	mirror := filepath.Join(home, ".claude-primary")
	if err := os.WriteFile(mirror, []byte("5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := fleet.PrimaryAccountOrImplicit(values, machine); got != 5 || err != nil {
		t.Fatalf("fleet.PrimaryAccountOrImplicit(mirror 5) = (%d, %v), want (5, nil)", got, err)
	}
	if err := os.Remove(mirror); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(mirror, 0o700); err != nil {
		t.Fatal(err)
	}
	if got, err := fleet.PrimaryAccountOrImplicit(values, machine); got != 3 || err == nil {
		t.Fatalf("fleet.PrimaryAccountOrImplicit(unreadable mirror %s) = (%d, %v), want (3, the read error)",
			mirror, got, err)
	}
}
