package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChatNewHarnessPromptRefusalsHappenBeforeSpawnAndWriteNothing(t *testing.T) {
	root := jailTest(t)
	workDir := filepath.Join(root, "work")
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		t.Fatal(err)
	}
	harness := filepath.Join(root, "alt.md")
	if err := os.WriteFile(harness, []byte("ALT"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The jail rosters no Codex or OpenCode account, so account resolution refuses those engines
	// first; the engine gate itself is pinned by TestLoadHarnessPromptForGatesTheEngineAndPassesAnEmptyPath.
	var got strings.Builder
	for _, test := range []struct{ engine, file string }{
		{"codex", harness}, {"opencode", harness}, {"claude", filepath.Join(root, "gone.md")},
	} {
		var stdout, stderr bytes.Buffer
		code := run([]string{
			"chat", "new", "--engine", test.engine, "--name", "alt", "--cwd", workDir, "--harness-prompt", test.file,
		}, &stdout, &stderr)
		fmt.Fprintf(&got, "%d %q %s%v\n", code, stdout.String(), stderr.String(),
			dirNames(t, os.Getenv("PFM_SID_DIR"), os.Getenv("PFM_TMUX_DIR")))
	}
	if got, want := rooted(
		got.String(),
		root,
	), "2 \"\" pfm chat new: requested Codex account 0 is not in the configured roster\n[]\n2 \"\" pfm chat new: OpenCode account 0 is not in the configured roster\n[]\n2 \"\" pfm chat new: harness prompt {root}/gone.md: stat {root}/gone.md: no such file or directory\n[]\n"; got != want {
		t.Fatalf("refusals = %q, want %q", got, want)
	}
}

// dirNames lists the entries of each directory, in order.
func dirNames(t *testing.T, dirs ...string) []string {
	t.Helper()
	var names []string
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
	}
	return names
}
