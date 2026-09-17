package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/paths"
	"hostops/pfm/internal/resolve"
)

func TestEpicInjectDedupeFollowsSessionAndEpicRename(t *testing.T) {
	setStoreTestJailForCommand(t)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"alpha", "beta"} {
		path := filepath.Join(root, "docs", "epics", slug, "manifest.md")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("manifest "+slug+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	payload, err := json.Marshal(map[string]string{
		"transcript_path": filepath.Join(root, "transcript.jsonl"),
		"cwd":             filepath.Join(root, "src", "nested"),
	})
	if err := os.MkdirAll(filepath.Join(root, "src", "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err != nil {
		t.Fatal(err)
	}

	window := "E_alpha_chat"
	oldIdentify := epicInjectIdentify
	oldWindow := epicInjectWindowName
	epicInjectIdentify = func(context.Context) (resolve.Identity, error) {
		return resolve.Identity{ID: "session-a", SocketPath: "/jail/cc", Pane: "%1"}, nil
	}
	epicInjectWindowName = func(context.Context, resolve.Identity) (string, error) {
		return window, nil
	}
	t.Cleanup(func() {
		epicInjectIdentify = oldIdentify
		epicInjectWindowName = oldWindow
	})

	call := func() (string, int) {
		var stdout, stderr bytes.Buffer
		code := runEpicInject(bytes.NewReader(payload), &stdout, &stderr)
		if code != 0 {
			t.Fatalf("epic inject code=%d stderr=%q", code, stderr.String())
		}
		return stdout.String(), code
	}
	first, _ := call()
	if !strings.Contains(first, "INJECTED EPIC alpha/manifest.md") ||
		!strings.Contains(first, "manifest alpha") {
		t.Fatalf("first injection = %q", first)
	}
	second, _ := call()
	if second != "" {
		t.Fatalf("same epic injected twice: %q", second)
	}
	window = "E_beta_chat"
	third, _ := call()
	if !strings.Contains(third, "INJECTED EPIC beta/manifest.md") {
		t.Fatalf("renamed epic injection = %q", third)
	}
	window = "E_alpha_chat"
	fourth, _ := call()
	if fourth != "" {
		t.Fatalf("renamed-back epic injected twice: %q", fourth)
	}
}

func setStoreTestJailForCommand(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	t.Setenv(paths.EnvDB, filepath.Join(root, "state", "fleet.db"))
	t.Setenv(paths.EnvSharedDB, filepath.Join(root, "cc", "fleet.db"))
	t.Setenv(paths.EnvSIDDir, filepath.Join(root, "sid"))
	t.Setenv(paths.EnvClaudeRoots, filepath.Join(root, "claude"))
	t.Setenv(paths.EnvCodexRoot, filepath.Join(root, "codex"))
	t.Setenv(paths.EnvTmuxDir, filepath.Join(root, "tmux"))
	t.Setenv(paths.EnvHome, filepath.Join(root, "home"))
}
