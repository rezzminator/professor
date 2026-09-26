package config

import (
	"path/filepath"
	"reflect"
	"testing"
)

// TestClaudeConfigDirsFollowsTheRosterOnce pins the query roster: roster order,
// the implicit account at {home}/.claude whatever its ConfigDir says, an
// account without a directory skipped, and a directory named twice kept once.
func TestClaudeConfigDirsFollowsTheRosterOnce(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	implicit := filepath.Join(home, ".claude")
	second := filepath.Join(home, ".cc", "2")
	third := filepath.Join(home, ".cc", "3")
	machine := Config{Accounts: []Account{
		{ID: 2, ConfigDir: second},
		{ID: 1, ConfigDir: filepath.Join(home, "ignored"), Implicit: true},
		{ID: 4},
		{ID: 5, ConfigDir: second},
		{ID: 6, ConfigDir: implicit},
		{ID: 3, ConfigDir: third},
	}}
	want := []string{second, implicit, third}
	if got := machine.ClaudeConfigDirs(home); !reflect.DeepEqual(got, want) {
		t.Fatalf("ClaudeConfigDirs() = %v, want %v", got, want)
	}
}

// TestClaudeConfigDirsEmptyRosterIsEmpty pins that no accounts yield no
// directories, never a guessed default home.
func TestClaudeConfigDirsEmptyRosterIsEmpty(t *testing.T) {
	if got := (Config{}).ClaudeConfigDirs(t.TempDir()); len(got) != 0 {
		t.Fatalf("ClaudeConfigDirs() = %v, want none", got)
	}
}
