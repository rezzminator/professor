package installer

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestCodexAppendixRejectsInvalidShapeBeforeMutation(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{`null`, `{"hooks":"personal"}`, `{"hooks":{"SessionStart":"personal"}}`, `{"hooks":{"SessionStart":[null]}}`, `{"hooks":{"SessionStart":[{"hooks":[null]}]}}`} {
		if _, _, _, err := updateCodexHooks(fixtureRetiredHooks, []byte(raw), t.TempDir(), false, nil); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

// An EXISTING install carries a retired SessionStart hook. An apply must take
// it away — not merely stop writing it — while an operator's own handler in
// the same shared, symlinked file is untouched.
func TestCodexApplyRetiresARetiredHookAndKeepsSharedSymlinks(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	retiredCommand := home + "/.local/bin/pfm internal compact-nudge"
	shared := filepath.Join(home, "shared-hooks.json")
	appendix, err := json.Marshal(map[string]any{
		"hooks": map[string]any{
			"Stop": []any{map[string]any{
				"hooks": []any{map[string]any{"type": "command", "command": "echo personal"}},
			}},
			"SessionStart": []any{map[string]any{
				"matcher": "startup",
				"hooks": []any{map[string]any{
					"type": "command", "command": retiredCommand, "timeout": 10,
				}},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, shared, string(appendix))
	homes := []string{filepath.Join(home, "account-a"), filepath.Join(home, "account-b")}
	for _, account := range homes {
		if err := os.MkdirAll(account, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(shared, filepath.Join(account, "hooks.json")); err != nil {
			t.Fatal(err)
		}
	}
	installer := engine{
		options: Options{
			Mode: ModeApply, Home: home, CodexHomes: homes, Stdout: io.Discard, retired: &fixtureRetiredHooks,
		},
		apply:       true,
		managedRoot: filepath.Join(home, "install"),
	}
	if err := installer.wireCodexHooks(); err != nil {
		t.Fatal(err)
	}
	for _, account := range homes {
		path := filepath.Join(account, "hooks.json")
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("symlink replaced: %s", path)
		}
		raw := readFixture(t, path)
		if count := hookCommandCount(t, raw, "SessionStart", retiredCommand); count != 0 {
			t.Fatalf("retired hook count=%d in %s:\n%s", count, path, raw)
		}
		if count := hookCommandCount(t, raw, "Stop", "echo personal"); count != 1 {
			t.Fatalf("personal hook lost from %s:\n%s", path, raw)
		}
	}
}

func TestCodexAppendixRefusesDanglingHookSymlink(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	account := filepath.Join(home, "account")
	if err := os.MkdirAll(account, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(account, "hooks.json")
	if err := os.Symlink(filepath.Join(home, "missing"), path); err != nil {
		t.Fatal(err)
	}
	installer := engine{
		options:     Options{Mode: ModeApply, Home: home, CodexHomes: []string{account}, Stdout: io.Discard},
		apply:       true,
		managedRoot: filepath.Join(home, "install"),
	}
	if err := installer.wireCodexHooks(); err == nil {
		t.Fatal("dangling link accepted")
	}
	if _, err := os.Readlink(path); err != nil {
		t.Fatalf("link replaced: %v", err)
	}
}
