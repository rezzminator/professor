package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadThemeDefaultsEmpty(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"version": 2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path, home, nil)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Claude.Theme != "" {
		t.Fatalf("Claude.Theme = %q with no key in the file, want empty", got.Claude.Theme)
	}
	if source := got.Source("claude.theme"); source != SourceDefault {
		t.Fatalf("Source(claude.theme) = %q, want %q", source, SourceDefault)
	}
}

func TestLoadThemeTopLevelEveryAccountInherits(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{
  "version": 2,
  "accounts": [
    {"id": 3, "configDir": "~/three"},
    {"id": 5, "configDir": "~/five", "claude": {"binary": "claude-five"}}
  ],
  "claude": {"theme": "dark"}
}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path, home, nil)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Claude.Theme != "dark" {
		t.Fatalf("Claude.Theme = %q, want dark", got.Claude.Theme)
	}
	if source := got.Source("claude.theme"); source != SourceFile {
		t.Fatalf("Source(claude.theme) = %q, want %q", source, SourceFile)
	}
	for _, id := range []int{3, 5} {
		if theme := got.EffectiveClaude(id).Theme; theme != "dark" {
			t.Fatalf("EffectiveClaude(%d).Theme = %q, want the inherited dark", id, theme)
		}
	}
}

func TestLoadThemePerAccountOverride(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{
  "version": 2,
  "accounts": [
    {"id": 3, "configDir": "~/three", "claude": {"theme": "light"}}
  ],
  "claude": {"theme": "dark"}
}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path, home, nil)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if theme := got.EffectiveClaude(3).Theme; theme != "light" {
		t.Fatalf("EffectiveClaude(3).Theme = %q, want the account override light", theme)
	}
	accountKey := "accounts[0].claude.theme"
	if source := got.Source(accountKey); source != SourceFile {
		t.Fatalf("Source(%s) = %q, want %q", accountKey, source, SourceFile)
	}
}

func TestLoadThemePerAccountUnsetInheritsTopLevel(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{
  "version": 2,
  "accounts": [
    {"id": 5, "configDir": "~/five", "claude": {"binary": "claude-five"}}
  ],
  "claude": {"theme": "dark"}
}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path, home, nil)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if theme := got.EffectiveClaude(5).Theme; theme != "dark" {
		t.Fatalf("EffectiveClaude(5).Theme = %q, want the inherited dark", theme)
	}
	if source := got.Source("accounts[0].claude.theme"); source != SourceDefault {
		t.Fatalf(
			"Source(accounts[0].claude.theme) = %q, want %q (no account-level key was set)",
			got.Source("accounts[0].claude.theme"),
			SourceDefault,
		)
	}
}

func TestLoadThemeRejectsWhitespaceOrEmpty(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	for _, body := range []string{
		`{"version": 2, "claude": {"theme": ""}}`,
		`{"version": 2, "claude": {"theme": "   "}}`,
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Load(path, home, nil)
		if err == nil {
			t.Fatalf("%s: Load() error = nil, want the empty-theme error", body)
		}
		if !strings.Contains(err.Error(), "claude.theme must be a non-empty string") {
			t.Fatalf("%s: Load() error = %v, want it to name claude.theme", body, err)
		}
	}
}

func TestLoadThemeRejectsWhitespaceOrEmptyPerAccount(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{
  "version": 2,
  "accounts": [{"id": 3, "configDir": "~/three", "claude": {"theme": "  "}}]
}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path, home, nil)
	if err == nil {
		t.Fatal("Load() error = nil, want the empty-theme error")
	}
	if !strings.Contains(err.Error(), "accounts[0].theme must be a non-empty string") {
		t.Fatalf("Load() error = %v, want it to name accounts[0].theme", err)
	}
}

func TestMarshalRoundTripsClaudeTheme(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{
  "version": 2,
  "accounts": [{"id": 3, "configDir": "~/three", "claude": {"theme": "light"}}],
  "claude": {"theme": "dark"}
}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path, home, nil)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	encoded, err := Marshal(got, false)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(encoded), `"theme": "dark"`) {
		t.Fatalf("Marshal dropped top-level claude.theme:\n%s", encoded)
	}
	if !strings.Contains(string(encoded), `"theme": "light"`) {
		t.Fatalf("Marshal dropped per-account claude.theme:\n%s", encoded)
	}
}
