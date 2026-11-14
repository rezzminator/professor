package reload

import (
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// TestClaudeRunWrapsAnInvalidEffortWithContext and its Codex twin pin that
// claudeRun/codexRun no longer return action.ClaudeEffort/CodexEffort's bare
// error unwrapped — a caller reading just the error string used to have no
// way to tell which phase of the respawn failed.
func TestClaudeRunWrapsAnInvalidEffortWithContext(t *testing.T) {
	_, err := claudeRun(Request{Engine: pfmengine.Claude, Effort: "not-a-real-effort"})
	if err == nil ||
		err.Error() != "resolve claude respawn effort: unknown Claude effort \"not-a-real-effort\" (want low, medium, high, xhigh or max)" {
		t.Fatalf("claudeRun() error = %v, want it wrapped with 'resolve claude respawn effort:'", err)
	}
}

func TestCodexRunWrapsAnInvalidEffortWithContext(t *testing.T) {
	_, err := codexRun(Request{Engine: pfmengine.Codex, Effort: "not-a-real-effort"})
	if err == nil ||
		err.Error() != "resolve codex respawn effort: unknown Codex effort \"not-a-real-effort\" (want minimal, low, medium, high, xhigh, max or ultra)" {
		t.Fatalf("codexRun() error = %v, want it wrapped with 'resolve codex respawn effort:'", err)
	}
}
