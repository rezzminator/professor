package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// everyRetiredKeyChanges judges every retired value as changing the launch; which values do is
// claudelaunch's call (RetiredKnobChangesLaunch).
func everyRetiredKeyChanges(string, json.RawMessage) bool { return true }

// The retired launch keys load as no-ops wherever the file sets them, each named for pfm doctor, and
// requireManagedCleanup still merges by scope.
func TestClaudeRetiredKeysLoadAsNoOps(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(t.TempDir(), FileName)
	content := `{"version":2,"claude":{"webSearchesPerSession":7,"autoCompactWindow":50000,` +
		`"helperModel":"claude-haiku-4-5","tmuxTruecolor":false,"cleanupPeriodDays":30,"nativeCursor":true,` +
		`"maxSubagentSpawnDepth":0,"maxConcurrentSubagents":"x","requireManagedCleanup":false},` +
		`"accounts":[{"id":1,"configDir":"~/one","claude":{"nativeCursor":false,"requireManagedCleanup":true}},` +
		`{"id":2,"configDir":"~/two"}]}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	for id, managed := range map[int]bool{1: true, 2: false} {
		if got := loaded.EffectiveClaude(id).RequireManagedCleanup; got != managed {
			t.Errorf("account %d requireManagedCleanup = %t, want %t", id, got, managed)
		}
	}
	want := []string{
		"accounts[0].claude.nativeCursor is retired: set claude.settings.env.CLAUDE_CODE_NATIVE_CURSOR (null turns it off) instead",
		"claude.autoCompactWindow is retired: set claude.settings.env.CLAUDE_CODE_AUTO_COMPACT_WINDOW instead",
		"claude.cleanupPeriodDays is retired: set claude.settings.cleanupPeriodDays instead",
		"claude.helperModel is retired: set claude.settings.env.ANTHROPIC_SMALL_FAST_MODEL instead",
		"claude.maxConcurrentSubagents is retired: set claude.settings.env.CLAUDE_CODE_MAX_CONCURRENT_SUBAGENTS instead",
		"claude.maxSubagentSpawnDepth is retired: set claude.settings.env.CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH instead",
		"claude.nativeCursor is retired: set claude.settings.env.CLAUDE_CODE_NATIVE_CURSOR (null turns it off) instead",
		"claude.tmuxTruecolor is retired: set claude.settings.env.CLAUDE_CODE_TMUX_TRUECOLOR (null turns it off) instead",
		"claude.webSearchesPerSession is retired: set claude.settings.env.CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION instead",
	}
	if got := loaded.RetiredKeyWarnings(everyRetiredKeyChanges); !reflect.DeepEqual(got, want) {
		t.Fatalf("RetiredKeyWarnings() =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	encoded, err := Marshal(loaded, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "helperModel") || strings.Contains(string(encoded), "nativeCursor") {
		t.Fatalf("Marshal wrote a retired key:\n%s", encoded)
	}
	clean := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(clean, []byte(`{"version":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err = Load(clean, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	if warnings := loaded.RetiredKeyWarnings(everyRetiredKeyChanges); len(warnings) != 0 {
		t.Fatalf("a file without retired keys: warnings %v", warnings)
	}
}
