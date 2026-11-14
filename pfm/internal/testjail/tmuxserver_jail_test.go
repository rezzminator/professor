//go:build jail

package testjail

import (
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// TestTmuxServerHoldsTheAllowanceUntilTheTestEnds: the real tmux is allowed
// inside the test that asked and released when it ends.
func TestTmuxServerHoldsTheAllowanceUntilTheTestEnds(t *testing.T) {
	t.Run("asks", func(t *testing.T) {
		TmuxServer(t)
		if !deps.AllowedInTest("tmux") {
			t.Fatal("AllowedInTest(tmux) inside TmuxServer's test = false, want true")
		}
	})
	if deps.AllowedInTest("tmux") {
		t.Fatal("AllowedInTest(tmux) after the asking test ended = true, want false")
	}
}
