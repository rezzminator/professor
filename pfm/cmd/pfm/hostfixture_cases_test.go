package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "hostops/pfm/internal/config"
	"hostops/pfm/internal/hostfixture"
)

// TestRunRefusesRealHomeUnderNoHomeFixture pins hostfixture case 1 (NoHome)
// for cmd/pfm's own top-level dispatcher: with HOME and PFM_HOME both
// unset, run() — the real command path every pfm invocation goes through —
// must refuse rather than silently resolve config against the operator's
// real home directory.
// Stays serial: hostfixture.NoHome jails through t.Setenv, which forbids a
// parallel test in the same body.
func TestRunRefusesRealHomeUnderNoHomeFixture(t *testing.T) {
	hostfixture.NoHome(t)

	var stdout, stderr strings.Builder
	code := run(nil, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run() under NoHome code=%d, want 1 (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "pfm: config:") {
		t.Fatalf("run() under NoHome stderr=%q, want a named config-resolution refusal", stderr.String())
	}
	if !strings.Contains(stderr.String(), "refusing to resolve") {
		t.Fatalf(
			"run() under NoHome stderr=%q, want the real-home refusal reason, not a generic failure",
			stderr.String(),
		)
	}
}

// TestIndexFollowsSymlinkedClaudeConfigDir pins hostfixture case 2
// (SymlinkedConfigDir) for cmd/pfm's own `pfm index`: the default Claude
// project root (home/.claude/projects) is a symlink to a physical directory
// elsewhere in the jail, and runIndex — the real command path — must index
// the transcript reached THROUGH that symlink, not treat the linked
// directory as empty or broken.
// Stays serial: hostfixture.SymlinkedConfigDir jails through t.Setenv, which
// forbids a parallel test in the same body.
func TestIndexFollowsSymlinkedClaudeConfigDir(t *testing.T) {
	fixture := hostfixture.SymlinkedConfigDir(t)

	const slug = "-jailed-project"
	const id = "60606060-6060-4060-8060-606060606060"
	projectDir := filepath.Join(fixture.Physical, "projects", slug)
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	transcriptPath := filepath.Join(projectDir, id+".jsonl")
	line := `{"type":"user","timestamp":"2026-01-01T00:00:00Z","sessionId":"` + id +
		`","message":{"role":"user","content":"hello through the symlink"}}` + "\n"
	if err := os.WriteFile(transcriptPath, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}

	runtime := commandRuntime{Paths: fixture.Values}
	var stdout, stderr strings.Builder
	if code := runIndex(nil, &stdout, &stderr, runtime, fixture.Clock); code != 0 {
		t.Fatalf(
			"runIndex() under SymlinkedConfigDir code=%d stdout=%q stderr=%q",
			code, stdout.String(), stderr.String(),
		)
	}
	if !strings.Contains(stdout.String(), "touched=1") {
		t.Fatalf(
			"runIndex() under SymlinkedConfigDir stdout=%q, want the transcript reached through ~/.claude's symlink counted",
			stdout.String(),
		)
	}
}

// TestUsageHookStaysSilentUnderNoCredsFixture pins hostfixture case 8
// (NoCreds) for cmd/pfm's own `pfm usage-hook`: an account with no
// .credentials.json anywhere on disk (and, on this platform, no keychain
// entry either) is not a usage-limit warning to print — the real command
// path must report nothing, not crash or emit a broken reminder for a seat
// that was never logged in.
// Stays serial: hostfixture.NoCreds jails through t.Setenv, which forbids a
// parallel test in the same body.
func TestUsageHookStaysSilentUnderNoCredsFixture(t *testing.T) {
	fixture := hostfixture.NoCreds(t)
	t.Setenv("CLAUDE_CONFIG_DIR", fixture.Values.Home)

	runtime := commandRuntime{
		Config: pfmconfig.Config{
			Accounts: []pfmconfig.Account{{ID: 1, ConfigDir: fixture.Values.Home}},
		},
	}
	var stdout, stderr strings.Builder
	code := runUsageHookWithRuntime(nil, &stdout, &stderr, runtime, fixture.Env)
	if code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf(
			"runUsageHookWithRuntime() under NoCreds code=%d stdout=%q stderr=%q, want silent fail-open",
			code, stdout.String(), stderr.String(),
		)
	}
}
