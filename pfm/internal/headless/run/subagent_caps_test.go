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
		want     string
	}{
		{nil, "-p --output-format json --settings {\"env\":{\"ANTHROPIC_SMALL_FAST_MODEL\":\"claude-haiku-5-5\",\"CLAUDE_CODE_AUTO_COMPACT_WINDOW\":\"100000\",\"CLAUDE_CODE_ENABLE_FUNCTION_HOOKS\":\"1\"},\"outputStyle\":\"default\"}"},
		{map[string]any{"maxSubagentSpawnDepth": 4, "maxConcurrentSubagents": 2}, "-p --output-format json --settings {\"env\":{\"ANTHROPIC_SMALL_FAST_MODEL\":\"claude-haiku-5-5\",\"CLAUDE_CODE_AUTO_COMPACT_WINDOW\":\"100000\",\"CLAUDE_CODE_ENABLE_FUNCTION_HOOKS\":\"1\",\"CLAUDE_CODE_MAX_CONCURRENT_SUBAGENTS\":\"2\",\"CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH\":\"4\"},\"outputStyle\":\"default\"}"},
	} {
		args, err := arguments(Request{Engine: pfmengine.Claude, Config: machine, Settings: test.settings})
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(args, " "); got != test.want {
			t.Errorf("settings %v argv = %q, want %q", test.settings, got, test.want)
		}
	}
}
