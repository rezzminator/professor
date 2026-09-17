package main

import (
	"testing"

	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/resolve"
)

// TestCallerEngine pins callerEngine's precedence with an injected fake
// getenv — never the real environment (pfm/CLAUDE.md § Testing Rules: no
// test may depend on the developer's ambient environment). The load-bearing
// case is "both set": CODEX_THREAD_ID is inherited into shells that are not
// themselves a Codex chat (a Claude Code chat's own tool shells carry it),
// so the Claude session variable must win whenever both are present.
func TestCallerEngine(t *testing.T) {
	fakeEnv := func(values map[string]string) func(string) string {
		return func(key string) string { return values[key] }
	}

	tests := []struct {
		name   string
		env    map[string]string
		want   pfmengine.ID
		wantOK bool
	}{
		{
			name:   "claude env only",
			env:    map[string]string{resolve.ClaudeSessionEnv: "claude-sess-1"},
			want:   pfmengine.Claude,
			wantOK: true,
		},
		{
			name:   "codex env only",
			env:    map[string]string{resolve.CodexThreadEnv: "codex-thread-1"},
			want:   pfmengine.Codex,
			wantOK: true,
		},
		{
			name: "both set favors claude because CODEX_THREAD_ID is inherited",
			env: map[string]string{
				resolve.ClaudeSessionEnv: "claude-sess-1",
				resolve.CodexThreadEnv:   "codex-thread-1",
			},
			want:   pfmengine.Claude,
			wantOK: true,
		},
		{
			name:   "neither set",
			env:    map[string]string{},
			want:   "",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := callerEngine(fakeEnv(tt.env))
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("callerEngine(%v) = (%q, %v), want (%q, %v)", tt.env, got, ok, tt.want, tt.wantOK)
			}
		})
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
