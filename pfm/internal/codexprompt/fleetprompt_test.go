package codexprompt

import (
	"strings"
	"testing"
)

// The composed prompt carries each of its three parts — shared head, Codex
// middle, shared tail — so a prompt missing one is as loud as one missing all.
func TestFleetPromptCarriesEveryPart(t *testing.T) {
	prompt, err := FleetPrompt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"# Model Selection", "NEVER change the active account", "# The Verdict"} {
		if !strings.Contains(prompt, marker) {
			t.Fatalf("the composed Codex prompt does not carry %q", marker)
		}
	}
}
