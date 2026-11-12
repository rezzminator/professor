package mockengine

import (
	"path/filepath"
	"strings"
	"testing"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
)

// TestClaudePermissionDialog pins Claude Code's tool permission dialog: PreToolUse, the tool_use record and
// PermissionRequest open it as a selector pfm reads; Yes runs the tool (PostToolUse, its result) and the
// turn goes on; No or Escape writes the rejection and the interrupt record pfm reads as an aborted turn, and
// fires neither PostToolUse nor Stop.
func TestClaudePermissionDialog(t *testing.T) {
	cases := []struct {
		name     string
		keys     string
		shown    string
		entries  []string
		hooks    string
		rejected bool
	}{
		{
			name:    "yes runs the tool and the turn goes on",
			keys:    "\r",
			shown:   "⏺ after",
			entries: []string{"user:go", "tool:", "assistant:after"},
			hooks:   "PreToolUse PermissionRequest PostToolUse Stop",
		},
		{
			name:     "no rejects the call",
			keys:     "2\r",
			shown:    "Interrupted · What should Claude do instead?",
			entries:  []string{"user:go", "tool:", "assistant:[turn aborted: interrupted]"},
			hooks:    "PreToolUse PermissionRequest",
			rejected: true,
		},
		{
			name:     "escape rejects the call",
			keys:     "\x1b",
			shown:    "Interrupted · What should Claude do instead?",
			entries:  []string{"user:go", "tool:", "assistant:[turn aborted: interrupted]"},
			hooks:    "PreToolUse PermissionRequest",
			rejected: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fix := newFixture(t)
			t.Chdir(fix.work)
			fired := filepath.Join(fix.root, "fired")
			hook := func(event string) string {
				return `"` + event + `":[{"hooks":[{"type":"command","command":"printf '%s ' ` + event + ` >> ` + fired + `"}]}]`
			}
			writeProjectHooks(t, fix.work, `{"hooks":{`+hook("PreToolUse")+","+hook("PermissionRequest")+","+
				hook("PostToolUse")+","+hook("Stop")+`}}`)
			fix.write(Scenario{SessionID: fixtureSession, BusyMS: intPtr(0), Steps: []Step{
				{Type: StepPermission, Tool: "Bash", Input: []byte(`{"command":"true"}`)},
				{Type: StepTurn, Reply: "after"},
			}})
			session := fix.startTUI("claude", claudeArgs(), nil)
			session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
			session.typeLine("go")
			frame := session.waitFrame("the permission dialog", func(frame string) bool {
				return pfmcall.SelectorLine(frame) != ""
			})
			if selector := pfmcall.SelectorLine(frame); !strings.Contains(selector, "1. Yes") {
				t.Fatalf("selector line = %q, want the preselected Yes row", selector)
			}
			session.typeRaw(tc.keys)
			session.waitFrame(tc.shown, func(frame string) bool {
				return strings.Contains(frame, tc.shown) && pfmcall.SelectorLine(frame) == ""
			})
			path := fix.claudeTranscript(fixtureSession)
			if got := entries(t, path); strings.Join(got, "\n") != strings.Join(tc.entries, "\n") {
				t.Fatalf("transcript %s entries = %q, want %q", path, got, tc.entries)
			}
			if got := strings.TrimSpace(readFile(t, fired)); got != tc.hooks {
				t.Fatalf("hooks fired = %q, want %q", got, tc.hooks)
			}
			content := readFile(t, path)
			if got := strings.Contains(
				content,
				`"content":"`+claudeToolRejection+`","is_error":true`,
			); got != tc.rejected {
				t.Fatalf("transcript %s holds the rejection tool_result: %t, want %t", path, got, tc.rejected)
			}
		})
	}
}

// TestClaudePermissionDeniedByAHookOpensNoDialog pins the PreToolUse deny: the call ends with its reason
// and no dialog opens.
func TestClaudePermissionDeniedByAHookOpensNoDialog(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	writeProjectHooks(t, fix.work,
		`{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"echo not here >&2; exit 2"}]}]}}`)
	fix.write(Scenario{SessionID: fixtureSession, BusyMS: intPtr(0), Steps: []Step{
		{Type: StepPermission, Tool: "Bash", Input: []byte(`{"command":"true"}`)},
	}})
	session := fix.startTUI("claude", claudeArgs(), nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	session.typeLine("go")
	frame := session.waitFrame("the denial", func(frame string) bool {
		return strings.Contains(frame, "⏺ Bash — denied by hook: not here")
	})
	if selector := pfmcall.SelectorLine(frame); selector != "" {
		t.Fatalf("a denied call opened the dialog: %q", selector)
	}
}
