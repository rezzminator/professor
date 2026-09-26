package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadCompactNudgeConfig(t *testing.T, claudeJSON, accountTwoClaudeJSON string) (Config, error) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{
  "version": 1,
  "accounts": [
    {"id": 1, "configDir": "~/one"},
    {"id": 2, "configDir": "~/two", "claude": ` + accountTwoClaudeJSON + `}
  ],
  "claude": ` + claudeJSON + `
}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(path, home, nil)
}

func TestCompactNudgeDefaultsOnAtThirtyFiveEveryTen(t *testing.T) {
	got, err := loadCompactNudgeConfig(t, `{"permissionMode": "prompted"}`, `{"permissionMode": "prompted"}`)
	if err != nil {
		t.Fatal(err)
	}
	want := CompactNudge{Enabled: true, Start: 35, Step: 10}
	if got.Claude.CompactNudge != want {
		t.Fatalf("Claude.CompactNudge = %+v, want %+v", got.Claude.CompactNudge, want)
	}
	for _, id := range []int{1, 2} {
		if effective := got.EffectiveClaude(id).CompactNudge; effective != want {
			t.Fatalf("EffectiveClaude(%d).CompactNudge = %+v, want the top-level default %+v", id, effective, want)
		}
	}
	if got.Source("claude.compactNudge.start") != SourceDefault {
		t.Fatalf("unset start reports source %q, want default", got.Source("claude.compactNudge.start"))
	}
}

func TestCompactNudgeFileAndAccountOverrides(t *testing.T) {
	got, err := loadCompactNudgeConfig(t,
		`{"compactNudge": {"enabled": false, "start": 50, "step": 20}}`,
		`{"compactNudge": {"step": 5}}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	if want := (CompactNudge{Enabled: false, Start: 50, Step: 20}); got.Claude.CompactNudge != want {
		t.Fatalf("Claude.CompactNudge = %+v, want %+v", got.Claude.CompactNudge, want)
	}
	if source := got.Source("claude.compactNudge.start"); source != SourceFile {
		t.Fatalf("file-set start reports source %q, want file", source)
	}
	if effective, want := got.EffectiveClaude(
		1,
	).CompactNudge, (CompactNudge{Enabled: false, Start: 50, Step: 20}); effective != want {
		t.Fatalf("EffectiveClaude(1).CompactNudge = %+v, want inherited %+v", effective, want)
	}
	// Account 2 touched only step: enabled and start come from the top level.
	if effective, want := got.EffectiveClaude(
		2,
	).CompactNudge, (CompactNudge{Enabled: false, Start: 50, Step: 5}); effective != want {
		t.Fatalf("EffectiveClaude(2).CompactNudge = %+v, want %+v", effective, want)
	}
}

func TestCompactNudgeRejectsPercentagesOutsideTheWindow(t *testing.T) {
	for _, test := range []struct{ claude, want string }{
		{`{"compactNudge": {"start": 0}}`, "compactNudge.start"},
		{`{"compactNudge": {"start": 101}}`, "compactNudge.start"},
		{`{"compactNudge": {"step": 0}}`, "compactNudge.step"},
		{`{"compactNudge": {"step": 101}}`, "compactNudge.step"},
	} {
		_, err := loadCompactNudgeConfig(t, test.claude, `{"permissionMode": "prompted"}`)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("Load(%s) error = %v, want a %s validation error", test.claude, err, test.want)
		}
	}
}
