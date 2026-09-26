package installer

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
)

// TestSubagentStatusLineIsWiredOnceAndOnlyOursIsRemoved pins the agent-panel
// row wiring: install writes the overlay's --subagents mode when the key is
// absent, never replaces an operator's own command, and uninstall removes
// exactly its own value.
func TestSubagentStatusLineIsWiredOnceAndOnlyOursIsRemoved(t *testing.T) {
	home := filepath.Join("neutral", "home")
	ours := SubagentStatusLineCommand(home)
	if ours != StatusLineOverlayCommand(home)+" --subagents" {
		t.Fatalf("SubagentStatusLineCommand=%q, want the overlay in --subagents mode", ours)
	}
	custom := `{"subagentStatusLine":{"type":"command","command":"my-rows"}}`
	for _, testCase := range []struct {
		name      string
		raw       string
		uninstall bool
		want      string // subagentStatusLine.command after the pass; "" = key absent
	}{
		{"install writes ours when absent", `{}`, false, ours},
		{"install keeps an operator's own command", custom, false, "my-rows"},
		{"uninstall removes ours", fmt.Sprintf(`{"subagentStatusLine":{"type":"command","command":%q}}`, ours), true, ""},
		{"uninstall keeps an operator's own command", custom, true, "my-rows"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			updated, _, _, err := updateSettings([]byte(testCase.raw), home, testCase.uninstall, nil)
			if err != nil {
				t.Fatal(err)
			}
			var document map[string]any
			if err := json.Unmarshal(updated, &document); err != nil {
				t.Fatal(err)
			}
			entry, present := document["subagentStatusLine"].(map[string]any)
			got, _ := entry["command"].(string)
			if testCase.want == "" {
				if present {
					t.Fatalf("subagentStatusLine still present after uninstall:\n%s", updated)
				}
				return
			}
			if got != testCase.want {
				t.Fatalf("subagentStatusLine.command=%q, want %q:\n%s", got, testCase.want, updated)
			}
			if kind, _ := entry["type"].(string); kind != "command" {
				t.Fatalf("subagentStatusLine.type=%q, want command:\n%s", kind, updated)
			}
		})
	}
}
