package main

import (
	"bytes"
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
	for _, testCase := range []struct {
		name, engine, file, want string
	}{
		// The jail rosters no Codex or OpenCode account, so account resolution
		// refuses those engines first; the engine gate itself is pinned by
		// TestLoadHarnessPromptForGatesTheEngineAndPassesAnEmptyPath.
		{name: "codex", engine: "codex", file: harness, want: "pfm chat new: "},
		{name: "opencode", engine: "opencode", file: harness, want: "pfm chat new: "},
		{name: "missing file", engine: "claude", file: filepath.Join(root, "gone.md"), want: "no such file"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run([]string{
				"chat", "new", "--engine", testCase.engine, "--name", "alt",
				"--cwd", workDir, "--harness-prompt", testCase.file,
			}, &stdout, &stderr)
			if code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), testCase.want) {
				t.Fatalf("run() exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			for _, directory := range []string{os.Getenv("PFM_SID_DIR"), os.Getenv("PFM_TMUX_DIR")} {
				entries, err := os.ReadDir(directory)
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range entries {
					if strings.HasPrefix(entry.Name(), "harness-prompt-") || strings.HasPrefix(entry.Name(), "cc-") ||
						strings.HasPrefix(entry.Name(), "cx-") {
						t.Fatalf("refused launch left %s in %s", entry.Name(), directory)
					}
				}
			}
		})
	}
}
