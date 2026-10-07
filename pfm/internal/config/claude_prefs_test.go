package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeLaunchPreferencesMergeAndValidateByScope(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(t.TempDir(), FileName)
	content := `{"version":2,"claude":{"webSearchesPerSession":7,"autoCompactWindow":50000,"tmuxTruecolor":false,"cleanupPeriodDays":30,"requireManagedCleanup":false},"accounts":[{"id":1,"configDir":"~/one","claude":{"webSearchesPerSession":9,"autoCompactWindow":250000,"tmuxTruecolor":true,"cleanupPeriodDays":14,"requireManagedCleanup":true}},{"id":2,"configDir":"~/two"}]}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id        int
		web       int64
		window    int64
		truecolor bool
		cleanup   int
		managed   bool
	}{{1, 9, 250000, true, 14, true}, {2, 7, 50000, false, 30, false}} {
		prefs := loaded.EffectiveClaude(tc.id)
		if prefs.WebSearchesPerSession != tc.web || prefs.AutoCompactWindow != tc.window ||
			prefs.TmuxTruecolor != tc.truecolor ||
			prefs.CleanupPeriodDays != tc.cleanup || prefs.RequireManagedCleanup != tc.managed {
			t.Fatalf("account %d launch preferences = %+v", tc.id, prefs)
		}
	}
	if loaded.Source("claude.autoCompactWindow") != SourceFile ||
		loaded.Source("accounts[0].claude.autoCompactWindow") != SourceFile {
		t.Fatalf("auto compact window sources = %#v", loaded.Sources)
	}
	encoded, err := Marshal(loaded, false)
	if err != nil || strings.Count(string(encoded), `"autoCompactWindow"`) != 2 {
		t.Fatalf("marshal auto compact window: err=%v config=%s", err, encoded)
	}
	for _, tc := range []struct{ content, want string }{
		{`{"version":2,"claude":{"webSearchesPerSession":0}}`, "claude.webSearchesPerSession"},
		{`{"version":2,"claude":{"autoCompactWindow":0}}`, "claude.autoCompactWindow must be at least 1"},
		{`{"version":2,"claude":{"cleanupPeriodDays":0}}`, "claude.cleanupPeriodDays"},
		{`{"version":2,"accounts":[{"id":1,"configDir":"~/one","claude":{"webSearchesPerSession":0}}]}`, "accounts[0].webSearchesPerSession"},
		{`{"version":2,"accounts":[{"id":1,"configDir":"~/one","claude":{"autoCompactWindow":0}}]}`, "accounts[0].autoCompactWindow must be at least 1"},
		{`{"version":2,"accounts":[{"id":1,"configDir":"~/one","claude":{"cleanupPeriodDays":0}}]}`, "accounts[0].cleanupPeriodDays"},
	} {
		if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path, home, nil); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("invalid %s error = %v", tc.want, err)
		} else if strings.Contains(tc.want, "autoCompactWindow") && err.Error() != "config "+path+": "+tc.want {
			t.Fatalf("invalid %s error = %v, want exact scope and path", tc.want, err)
		}
	}
}
