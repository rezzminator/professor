//go:build jail

package mockengine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
)

// TestClaudeTurnEndsShortOfAReply pins how a Claude turn ends without its
// reply, each read back through pfm's own transcript parser: Escape leaves the
// interrupt record and no Stop hook; an api_error step leaves the synthetic
// error reply, no Stop hook, and the composer live for the next prompt.
func TestClaudeTurnEndsShortOfAReply(t *testing.T) {
	cases := []struct {
		name    string
		steps   []Step
		escape  bool
		shown   string
		entries []string
	}{
		{
			name:    "escape mid-turn",
			steps:   []Step{{Type: StepTurn, Reply: "never", BusyMS: 30000}},
			escape:  true,
			shown:   "⎿ Interrupted",
			entries: []string{"user:go", "assistant:[turn aborted: interrupted]"},
		},
		{
			name:    "api error with its defaults",
			steps:   []Step{{Type: StepAPIError}},
			shown:   "⎿ " + defaultAPIErrorText,
			entries: []string{"user:go", "assistant:" + defaultAPIErrorText + ":server_error"},
		},
		{
			name:    "api error with its own text and kind",
			steps:   []Step{{Type: StepAPIError, Reply: "API Error: 429 rate limited", Error: "rate_limit"}},
			shown:   "⎿ API Error: 429 rate limited",
			entries: []string{"user:go", "assistant:API Error: 429 rate limited:rate_limit"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fix := newFixture(t)
			t.Chdir(fix.work)
			stopped := filepath.Join(fix.root, "stopped")
			project := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"touch ` + stopped + `"}]}]}}`
			if err := os.MkdirAll(filepath.Join(fix.work, ".claude"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(
				filepath.Join(fix.work, ".claude", "settings.json"),
				[]byte(project),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
			fix.write(
				Scenario{SessionID: fixtureSession, Steps: append(tc.steps, Step{Type: StepTurn, Reply: "after"})},
			)
			session := fix.startTUI("claude", claudeArgs(), nil)
			session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
			session.typeLine("go")
			if tc.escape {
				session.waitFrame("the busy footer", pfmcall.IsBusy)
				session.typeRaw("\x1b")
			}
			session.waitFrame(tc.shown, func(frame string) bool {
				return !pfmcall.IsBusy(frame) && strings.Contains(frame, tc.shown)
			})
			path := fix.claudeTranscript(fixtureSession)
			if got := entries(t, path); strings.Join(got, "\n") != strings.Join(tc.entries, "\n") {
				t.Fatalf("transcript %s entries = %q, want %q", path, got, tc.entries)
			}
			if _, err := os.Stat(stopped); !os.IsNotExist(err) {
				t.Fatalf("a Stop hook ran for a turn that ended short of its reply (stat %s: %v)", stopped, err)
			}
			session.typeLine("again")
			session.waitFrame("the next reply", func(frame string) bool { return strings.Contains(frame, "⏺ after") })
			if _, err := os.Stat(stopped); err != nil {
				t.Fatalf("the next turn's Stop hook did not run: %v", err)
			}
		})
	}
}
