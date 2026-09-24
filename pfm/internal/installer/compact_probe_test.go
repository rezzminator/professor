package installer

import (
	"os"
	"path/filepath"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// With both auto-compact thresholds set, doctor proves the window pfm install
// writes: env.CLAUDE_CODE_AUTO_COMPACT_WINDOW holds the lower threshold in
// every Claude settings file, and any other value or none is drift.
func TestProbeExpectedHooksChecksTheCompactWindow(t *testing.T) {
	for _, testCase := range []struct {
		name, settings, state, got string
	}{
		{name: "the lower threshold", settings: `{"env":{"CLAUDE_CODE_AUTO_COMPACT_WINDOW":"100000"}}`, state: stateOK},
		{
			name: "another value", settings: `{"env":{"CLAUDE_CODE_AUTO_COMPACT_WINDOW":"200000"}}`,
			state: stateHookDrift, got: "200000",
		},
		{name: "no env block", settings: `{}`, state: stateHookDrift, got: compactWindowAbsent},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			configDir := filepath.Join(home, ".claude")
			if err := os.MkdirAll(configDir, 0o755); err != nil {
				t.Fatal(err)
			}
			settings := filepath.Join(configDir, "settings.json")
			if err := os.WriteFile(settings, []byte(testCase.settings), 0o600); err != nil {
				t.Fatal(err)
			}
			machine := pfmconfig.Config{Accounts: []pfmconfig.Account{{ID: 1, ConfigDir: configDir}}}
			machine.Claude.AutoCompactMain, machine.Claude.AutoCompactSubagent = 150000, 100000
			var row *HookProbeResult
			results := ProbeExpectedHooks(home, machine)
			for index := range results {
				if results[index].Hook.Name == compactWindowProbeName {
					row = &results[index]
				}
			}
			if row == nil {
				t.Fatalf("no %s row among %d probe rows", compactWindowProbeName, len(results))
			}
			if row.State != testCase.state || row.Hook.File != settings {
				t.Fatalf("row = %+v, want state %s for %s", *row, testCase.state, settings)
			}
			if testCase.state == stateHookDrift &&
				(row.What != autoCompactWindowEnv || row.Want != "100000" || row.Got != testCase.got) {
				t.Fatalf("drift = %s want=%s got=%s, want %s want=100000 got=%s",
					row.What, row.Want, row.Got, autoCompactWindowEnv, testCase.got)
			}
		})
	}
}
