package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadNativeCursorDefaultsFalse(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"version": 2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path, home, nil)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Claude.NativeCursor {
		t.Fatal("Claude.NativeCursor = true with no key in the file, want false")
	}
	if source := got.Source("claude.nativeCursor"); source != SourceDefault {
		t.Fatalf("Source(claude.nativeCursor) = %q, want %q", source, SourceDefault)
	}
}

func TestLoadNativeCursorTopLevelAndPerAccount(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{
  "version": 2,
  "accounts": [
    {"id": 3, "configDir": "~/three", "claude": {"nativeCursor": false}},
    {"id": 5, "configDir": "~/five", "claude": {"binary": "claude-five"}}
  ],
  "claude": {"nativeCursor": true}
}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path, home, nil)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !got.Claude.NativeCursor {
		t.Fatal("Claude.NativeCursor = false, want the top-level true")
	}
	if source := got.Source("claude.nativeCursor"); source != SourceFile {
		t.Fatalf("Source(claude.nativeCursor) = %q, want %q", source, SourceFile)
	}
	if got.EffectiveClaude(3).NativeCursor {
		t.Fatal("EffectiveClaude(3).NativeCursor = true, want the account false")
	}
	accountKey := "accounts[0].claude.nativeCursor"
	if source := got.Source(accountKey); source != SourceFile {
		t.Fatalf("Source(%s) = %q, want %q", accountKey, source, SourceFile)
	}
	// Account 5 touches only binary: it must inherit the top-level true,
	// not the bool zero value.
	if !got.EffectiveClaude(5).NativeCursor {
		t.Fatal("EffectiveClaude(5).NativeCursor = false, want the inherited true")
	}
}
