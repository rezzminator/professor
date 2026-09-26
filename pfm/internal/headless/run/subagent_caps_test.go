package run

import (
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestSubagentCapsRequirePassedSettings(t *testing.T) {
	machine := pfmconfig.Config{Claude: pfmconfig.ClaudePrefs{MaxSubagentSpawnDepth: 9, MaxConcurrentSubagents: 32}}
	for _, test := range []struct {
		settings map[string]any
		want     bool
	}{
		{nil, false},
		{map[string]any{"maxSubagentSpawnDepth": 4, "maxConcurrentSubagents": 2}, true},
	} {
		args, err := arguments(Request{Engine: pfmengine.Claude, Config: machine, Settings: test.settings})
		if err != nil {
			t.Fatal(err)
		}
		payload := strings.Join(args, " ")
		if strings.Contains(payload, "CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH") != test.want ||
			strings.Contains(payload, "CLAUDE_CODE_MAX_CONCURRENT_SUBAGENTS") != test.want {
			t.Fatalf("settings=%v, argv=%q", test.settings, args)
		}
	}
}
