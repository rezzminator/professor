package testjail

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/codexgen"
)

// StageGlobalAgents wires the checkout's current role roster into a doctor jail.
func StageGlobalAgents(t *testing.T, home string) {
	t.Helper()
	// A healthy clone-backed registry also carries the real transcript script
	// that doctor audits and chat_digest uses at its installed fallback.
	transcript := filepath.Join(home, ".claude", "skills", "transcript", "transcript.py")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(transcript); os.IsNotExist(err) {
		if err := os.Symlink(
			filepath.Join(checkoutRoot(), "templates", "global", "skills", "transcript", "transcript.py"),
			transcript,
		); err != nil {
			t.Fatal(err)
		}
	} else if err != nil {
		t.Fatal(err)
	}
	_, err := codexgen.RunGlobalAgents(codexgen.GlobalAgentsOptions{
		Home: home, SourceRepo: checkoutRoot(),
		ClaudeConfigDirs: []string{filepath.Join(home, ".claude")},
		CodexHomes:       []string{filepath.Join(home, ".codex")}, Mode: codexgen.ModeBuild,
	})
	if err != nil {
		t.Fatal(err)
	}
}
