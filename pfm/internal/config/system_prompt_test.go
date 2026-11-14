package config

import (
	"os"
	"path/filepath"
	"testing"
)

func loadSystemPromptConfig(t *testing.T, claudeJSON string) (Config, error) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{
  "version": 1,
  "accounts": [
    {"id": 1, "configDir": "~/one"},
    {"id": 2, "configDir": "~/two", "claude": {"systemPrompt": "professor"}}
  ],
  "claude": ` + claudeJSON + `
}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(path, home, nil)
}

func TestSystemPromptValidValuesResolve(t *testing.T) {
	for _, value := range []string{SystemPromptProduction, SystemPromptLean, SystemPromptProfessor} {
		got, err := loadSystemPromptConfig(t, `{"systemPrompt": "`+value+`"}`)
		if err != nil {
			t.Fatalf("Load(systemPrompt=%q) error = %v", value, err)
		}
		if got.Claude.SystemPrompt != value {
			t.Fatalf("Claude.SystemPrompt = %q, want %q", got.Claude.SystemPrompt, value)
		}
	}
}

func TestSystemPromptAbsentMeansProductionAndAccountsOverride(t *testing.T) {
	got, err := loadSystemPromptConfig(t, `{"permissionMode": "prompted"}`)
	if err != nil {
		t.Fatalf("Load error = %v", err)
	}
	if got.Claude.SystemPrompt != "" {
		t.Fatalf("absent systemPrompt resolved to %q, want empty (production)", got.Claude.SystemPrompt)
	}
	if effective := got.EffectiveClaude(1).SystemPrompt; effective != "" {
		t.Fatalf("EffectiveClaude(1).SystemPrompt = %q, want empty (inherit production)", effective)
	}
	if effective := got.EffectiveClaude(2).SystemPrompt; effective != SystemPromptProfessor {
		t.Fatalf("EffectiveClaude(2).SystemPrompt = %q, want %q (account override)", effective, SystemPromptProfessor)
	}
}
