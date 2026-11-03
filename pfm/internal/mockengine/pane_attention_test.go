package mockengine

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestPermissionHookDecisionResumesWithoutAKey(t *testing.T) {
	fix := newFixture(t)
	fix.write(
		Scenario{
			Steps: []Step{
				{Type: StepPermission, Tool: "Bash", Input: json.RawMessage(`{"command":"pwd"}`)},
				{Type: StepTurn, Reply: "allowed by hook"},
			},
		},
	)
	writeHookSettings(
		t,
		filepath.Join(fix.configDir, "settings.json"),
		map[string]any{
			hookPermissionRequest: commandHook(
				`printf '%s' '{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}'`,
			),
		},
	)
	session := fix.startTUI(engineClaude, nil, nil)
	session.waitFrame("composer", func(s string) bool { return strings.Contains(s, "❯") })
	session.typeLine("inspect atlas")
	session.waitFrame("hook-approved reply", func(s string) bool { return strings.Contains(s, "allowed by hook") })
	session.typeLine("/exit")
	if code := session.waitExit(); code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
}
