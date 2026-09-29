package codexgen

import "testing"

// A role with no Codex pin runs the model its Claude alias maps to: the smart
// and apex aliases on the current Sol, the mechanical and collector ones on Luna.
func TestDefaultModelMapFollowsTheFleetTiers(t *testing.T) {
	want := map[string]string{
		"opus":   "gpt-6.1-sol",
		"fable":  "gpt-6.1-sol",
		"sonnet": "gpt-6-luna",
		"haiku":  "gpt-6-luna",
	}
	got := defaultConfig().ModelMap
	for alias, model := range want {
		if got[alias] != model {
			t.Errorf("default Codex model for %q = %q, want %q", alias, got[alias], model)
		}
	}
	if len(got) != len(want) {
		t.Errorf("default Codex model map = %v, want exactly %v", got, want)
	}
}
