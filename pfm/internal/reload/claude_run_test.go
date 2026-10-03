package reload

import (
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

func TestClaudeRunCarriesTheChatLabel(t *testing.T) {
	home := t.TempDir()
	const id = "11111111-1111-4111-8111-111111111111"
	for _, scenario := range []struct {
		name  string
		fresh bool
		label string
	}{
		{"same-pane resume with a label", false, "Fix login"},
		{"fresh launch with a label", true, "Fix login"},
		{"same-pane resume without a label", false, ""},
		{"fresh launch without a label", true, ""},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			run, err := claudeRun(Request{
				Account:   2,
				Home:      home,
				Machine:   reloadTestMachine(pfmconfig.SystemPromptLean, home),
				SessionID: id,
				Name:      scenario.label,
				fresh:     scenario.fresh,
			})
			if err != nil {
				t.Fatal(err)
			}
			parsed := parsedReloadShell(t, run)
			if parsed.Name != scenario.label {
				t.Fatalf("--name = %q, want %q", parsed.Name, scenario.label)
			}
			if scenario.label == "" && strings.Contains(run, "'--name'") {
				t.Fatal("an unlabelled reload carries --name")
			}
			if scenario.fresh && parsed.SessionID != id || !scenario.fresh && parsed.Resume != id {
				t.Fatalf("reload lost its session identity: session %q resume %q", parsed.SessionID, parsed.Resume)
			}
		})
	}
}
