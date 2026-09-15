package installer

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestCodexAppendixRejectsInvalidShapeBeforeMutation(t *testing.T) {
	for _, raw := range []string{`null`, `{"hooks":"personal"}`, `{"hooks":{"SessionStart":"personal"}}`, `{"hooks":{"SessionStart":[null]}}`, `{"hooks":{"SessionStart":[{"hooks":[null]}]}}`} {
		if _, _, _, err := updateCodexHooks([]byte(raw), t.TempDir(), false, nil); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestCodexAppendixPreservesSharedHookSymlinks(t *testing.T) {
	home := t.TempDir()
	shared := filepath.Join(home, "shared-hooks.json")
	writeFixture(t, shared, `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo personal"}]}]}}`)
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
		options:     Options{Mode: ModeApply, Home: home, CodexHomes: homes, Stdout: io.Discard},
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
		if count := hookCommandCount(
			t,
			readFixture(t, path),
			"SessionStart",
			codexHookTemplate(home).Command,
		); count != 1 {
			t.Fatalf("account appendix count=%d", count)
		}
	}
	installer.options.Mode = ModeUninstall
	if err := installer.wireCodexHooks(); err != nil {
		t.Fatal(err)
	}
	raw := readFixture(t, shared)
	if count := hookCommandCount(t, raw, "Stop", "echo personal"); count != 1 {
		t.Fatalf("personal hook lost: %s", raw)
	}
	if count := hookCommandCount(t, raw, "SessionStart", codexHookTemplate(home).Command); count != 0 {
		t.Fatalf("owned hook retained: %s", raw)
	}
}

func TestCodexAppendixRefusesDanglingHookSymlink(t *testing.T) {
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
