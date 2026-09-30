package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// jailInjectEnv isolates chat injection from the host fleet.
func jailInjectEnv(t *testing.T) {
	t.Helper()
	root := testjail.ShortRoot(t)
	home := filepath.Join(root, "home")
	for _, directory := range []string{
		home,
		filepath.Join(root, "claude"),
		filepath.Join(root, "codex"),
		filepath.Join(root, "tmux"),
		filepath.Join(root, "proc"),
		filepath.Join(root, "sid"),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("TMUX", "")
	t.Setenv("PFM_HOME", home)
	t.Setenv("PFM_CACHE_DB", filepath.Join(root, "pfm-cache.db"))
	t.Setenv("PFM_STATE_DB", filepath.Join(root, "shared.db"))
	t.Setenv("PFM_SID_DIR", filepath.Join(root, "sid"))
	t.Setenv("PFM_CLAUDE_ROOTS", filepath.Join(root, "claude"))
	t.Setenv("PFM_CODEX_ROOT", filepath.Join(root, "codex"))
	t.Setenv("PFM_TMUX_DIR", filepath.Join(root, "tmux"))
	t.Setenv("PFM_PROC_ROOT", filepath.Join(root, "proc"))
	statedTestSender(t)
}

// TestChatInjectRefusesCompactBeforeAnyResolveOrEngine checks the refusal before target resolution.
func TestChatInjectRefusesCompactBeforeAnyResolveOrEngine(t *testing.T) {
	jailInjectEnv(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"chat", "inject", "--then", "resume the wave",
		"no-such-target", "/compact", "hold:", "state",
	}, &stdout, &stderr)
	if code != codeUndelivered {
		t.Fatalf(
			"exit=%d stdout=%q stderr=%q, want codeUndelivered (%d)",
			code,
			stdout.String(),
			stderr.String(),
			codeUndelivered,
		)
	}
	if stdout.Len() != 0 {
		t.Fatalf("a banned /compact primary printed to stdout as if delivered: %q", stdout.String())
	}
	for _, want := range []string{"/compact is never injected", "pfm never types a compaction"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("refusal %q lacks %q", stderr.String(), want)
		}
	}
}
