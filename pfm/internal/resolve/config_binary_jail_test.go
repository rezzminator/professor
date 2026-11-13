//go:build jail

package resolve

import (
	"testing"
)

func TestEngineCommandBasenamesAcceptConfiguredPaths(t *testing.T) {
	customClaude := "/opt/tools/claude enterprise"
	customCodex := "/opt/tools/codex safe"
	for _, test := range []struct {
		name    string
		command string
		check   func(string, ...string) bool
		binary  string
	}{
		{name: "claude", command: customClaude, check: isClaudePaneCommand, binary: customClaude},
		{name: "codex", command: customCodex, check: isCodexPaneCommand, binary: customCodex},
	} {
		t.Run(test.name, func(t *testing.T) {
			if !test.check(test.command, test.binary) {
				t.Fatalf("configured command %q was not recognized", test.command)
			}
		})
	}
	if isCodexPaneCommand(customCodex) {
		t.Fatal("custom Codex basename was accepted without its configured policy")
	}
}
