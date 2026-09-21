package main

import (
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/resolve"
)

// clearCallerEngineEnv isolates a test's empty-engine resolution from this
// shell's own ambient session env: resolveRunEngineAccount now consults
// callerEngine(os.Getenv) before falling to the config default, so a
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
