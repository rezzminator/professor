package reload

import (
	"strings"
	"testing"

	pfmengine "hostops/pfm/internal/engine"
)

// TestClaudeRunWrapsAnInvalidEffortWithContext and its Codex twin pin that
// claudeRun/codexRun no longer return action.ClaudeEffort/CodexEffort's bare
// error unwrapped — a caller reading just the error string used to have no
// way to tell which phase of the respawn failed.
func TestClaudeRunWrapsAnInvalidEffortWithContext(t *testing.T) {
	_, err := claudeRun(Request{Engine: pfmengine.Claude, Effort: "not-a-real-effort"})
	if err == nil || !strings.Contains(err.Error(), "resolve claude respawn effort:") {
		t.Fatalf("claudeRun() error = %v, want it wrapped with 'resolve claude respawn effort:'", err)
	}
}

func TestCodexRunWrapsAnInvalidEffortWithContext(t *testing.T) {
	_, err := codexRun(Request{Engine: pfmengine.Codex, Effort: "not-a-real-effort"})
	if err == nil || !strings.Contains(err.Error(), "resolve codex respawn effort:") {
		t.Fatalf("codexRun() error = %v, want it wrapped with 'resolve codex respawn effort:'", err)
	}
}
