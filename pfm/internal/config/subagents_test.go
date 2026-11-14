package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSubagentConfig(t *testing.T, content string) Config {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path, home, nil)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return got
}

func TestLoadSubagentCapsDefault(t *testing.T) {
	got := writeSubagentConfig(t, `{"version": 2}`)
	if got.Claude.MaxSubagentSpawnDepth != DefaultSubagentSpawnDepth {
		t.Fatalf(
			"Claude.MaxSubagentSpawnDepth = %d with no key in the file, want %d",
			got.Claude.MaxSubagentSpawnDepth,
			DefaultSubagentSpawnDepth,
		)
	}
	if got.Claude.MaxConcurrentSubagents != 0 {
		t.Fatalf(
			"Claude.MaxConcurrentSubagents = %d with no key in the file, want 0 (not passed)",
			got.Claude.MaxConcurrentSubagents,
		)
	}
	for _, key := range []string{"claude.maxSubagentSpawnDepth", "claude.maxConcurrentSubagents"} {
		if source := got.Source(key); source != SourceDefault {
			t.Fatalf("Source(%s) = %q, want %q", key, source, SourceDefault)
		}
	}
}

func TestLoadSubagentCapsTopLevelAndPerAccount(t *testing.T) {
	got := writeSubagentConfig(t, `{
  "version": 2,
  "accounts": [
    {"id": 3, "configDir": "~/three", "claude": {"maxSubagentSpawnDepth": 5}},
    {"id": 5, "configDir": "~/five", "claude": {"binary": "claude-five"}}
  ],
  "claude": {"maxSubagentSpawnDepth": 9, "maxConcurrentSubagents": 32}
}`)
	if got.Claude.MaxSubagentSpawnDepth != 9 || got.Claude.MaxConcurrentSubagents != 32 {
		t.Fatalf(
			"top-level caps = %d/%d, want 9/32",
			got.Claude.MaxSubagentSpawnDepth,
			got.Claude.MaxConcurrentSubagents,
		)
	}
	for _, key := range []string{"claude.maxSubagentSpawnDepth", "claude.maxConcurrentSubagents"} {
		if source := got.Source(key); source != SourceFile {
			t.Fatalf("Source(%s) = %q, want %q", key, source, SourceFile)
		}
	}
	// Account 3 overrides only the depth: the concurrency cap must still be
	// the inherited 32, not the int zero value.
	if prefs := got.EffectiveClaude(3); prefs.MaxSubagentSpawnDepth != 5 || prefs.MaxConcurrentSubagents != 32 {
		t.Fatalf(
			"EffectiveClaude(3) caps = %d/%d, want 5/32",
			prefs.MaxSubagentSpawnDepth,
			prefs.MaxConcurrentSubagents,
		)
	}
	accountKey := "accounts[0].claude.maxSubagentSpawnDepth"
	if source := got.Source(accountKey); source != SourceFile {
		t.Fatalf("Source(%s) = %q, want %q", accountKey, source, SourceFile)
	}
	// Account 5 touches only binary: it inherits both.
	if prefs := got.EffectiveClaude(5); prefs.MaxSubagentSpawnDepth != 9 || prefs.MaxConcurrentSubagents != 32 {
		t.Fatalf(
			"EffectiveClaude(5) caps = %d/%d, want the inherited 9/32",
			prefs.MaxSubagentSpawnDepth,
			prefs.MaxConcurrentSubagents,
		)
	}
}
