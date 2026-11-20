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
		if got.Claude.SystemPrompt != value || got.Claude.SystemPromptDefault {
			t.Fatalf("Claude.SystemPrompt = %q default=%t, want %q default=false",
				got.Claude.SystemPrompt, got.Claude.SystemPromptDefault, value)
		}
	}
}

func TestSystemPromptUnsetMeansProfessorAndAccountsOverride(t *testing.T) {
	for _, test := range []struct{ name, claude string }{
		{name: "absent", claude: `{"permissionMode": "prompted"}`},
		{name: "null", claude: `{"systemPrompt": null}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := loadSystemPromptConfig(t, test.claude)
			if err != nil {
				t.Fatalf("Load error = %v", err)
			}
			if got.Claude.SystemPrompt != SystemPromptProfessor {
				t.Fatalf(
					"%s systemPrompt resolved to %q, want %q",
					test.name,
					got.Claude.SystemPrompt,
					SystemPromptProfessor,
				)
			}
			if effective := got.EffectiveClaude(1); effective.SystemPrompt != SystemPromptProfessor ||
				!effective.SystemPromptDefault {
				t.Fatalf("EffectiveClaude(1) systemPrompt=%q default=%t, want %q default=true (inherit)",
					effective.SystemPrompt, effective.SystemPromptDefault, SystemPromptProfessor)
			}
			if effective := got.EffectiveClaude(2); effective.SystemPrompt != SystemPromptProfessor ||
				effective.SystemPromptDefault {
				t.Fatalf("EffectiveClaude(2) systemPrompt=%q default=%t, want %q default=false (account sets it)",
					effective.SystemPrompt, effective.SystemPromptDefault, SystemPromptProfessor)
			}
		})
	}
	got, err := loadSystemPromptConfig(t, `{"systemPrompt": "production"}`)
	if err != nil {
		t.Fatalf("Load error = %v", err)
	}
	if effective := got.EffectiveClaude(2).SystemPrompt; effective != SystemPromptProfessor {
		t.Fatalf("EffectiveClaude(2).SystemPrompt = %q, want %q (account override)", effective, SystemPromptProfessor)
	}
	if effective := got.EffectiveClaude(1).SystemPrompt; effective != SystemPromptProduction {
		t.Fatalf("EffectiveClaude(1).SystemPrompt = %q, want %q (explicit opt-out)", effective, SystemPromptProduction)
	}
}
