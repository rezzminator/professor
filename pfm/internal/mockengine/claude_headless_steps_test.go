package mockengine

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestClaudeHeadlessExecutesScriptedHooks(t *testing.T) {
	fix := newFixture(t)
	trace := filepath.Join(fix.root, "headless-events")
	hooks := map[string]any{}
	for _, event := range []string{hookSessionStart, hookUserPromptSubmit, hookPreToolUse, hookPostToolUse, hookStop, hookSessionEnd} {
		hooks[event] = commandHook("printf '%s\\n' " + event + " >> " + trace)
	}
	writeHookSettings(t, filepath.Join(fix.configDir, "settings.json"), hooks)
	fix.write(
		Scenario{
			Steps: []Step{
				{Type: StepToolCall, Tool: "Bash", Input: json.RawMessage(`{"command":"pwd"}`)},
				{Type: StepTurn, Reply: "done"},
			},
		},
	)
	code, out, stderr := runOnce(fix, engineClaude, []string{"-p", "inspect atlas"}, "")
	if code != 0 || out != "done\n" || stderr != "" {
		t.Fatalf("headless=(%d,%q,%q), want (0,done\\n,empty)", code, out, stderr)
	}
	expected := "SessionStart\nUserPromptSubmit\nPreToolUse\nPostToolUse\nStop\nSessionEnd\n"
	if actual := readFile(t, trace); actual != expected {
		t.Fatalf("%s: events=%q, want %q", trace, actual, expected)
	}
}
