package run

import (
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestSubagentCapsRequirePassedSettings(t *testing.T) {
	machine := pfmconfig.Config{Claude: pfmconfig.ClaudePrefs{Settings: map[string]any{"env": map[string]any{
		"CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH": "9", "CLAUDE_CODE_MAX_CONCURRENT_SUBAGENTS": "32",
	}}}}
	for _, test := range []struct {
		settings map[string]any
		want     string
	}{
		{nil, "-p --output-format json --settings {\"cleanupPeriodDays\":36500,\"env\":{\"ANTHROPIC_SMALL_FAST_MODEL\":\"claude-haiku-5-5\",\"CLAUDE_CODE_AUTO_COMPACT_WINDOW\":\"100000\",\"CLAUDE_CODE_ENABLE_FUNCTION_HOOKS\":\"1\",\"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS\":\"0\",\"CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH\":\"8\",\"CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION\":\"9007199254740991\",\"CLAUDE_CODE_NATIVE_CURSOR\":\"1\",\"CLAUDE_CODE_TMUX_TRUECOLOR\":\"1\"},\"outputStyle\":\"default\",\"showThinkingSummaries\":true,\"tui\":\"fullscreen\"}"},
		{map[string]any{"maxSubagentSpawnDepth": 4, "maxConcurrentSubagents": 2}, "-p --output-format json --settings {\"cleanupPeriodDays\":36500,\"env\":{\"ANTHROPIC_SMALL_FAST_MODEL\":\"claude-haiku-5-5\",\"CLAUDE_CODE_AUTO_COMPACT_WINDOW\":\"100000\",\"CLAUDE_CODE_ENABLE_FUNCTION_HOOKS\":\"1\",\"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS\":\"0\",\"CLAUDE_CODE_MAX_CONCURRENT_SUBAGENTS\":\"2\",\"CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH\":\"4\",\"CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION\":\"9007199254740991\",\"CLAUDE_CODE_NATIVE_CURSOR\":\"1\",\"CLAUDE_CODE_TMUX_TRUECOLOR\":\"1\"},\"outputStyle\":\"default\",\"showThinkingSummaries\":true,\"tui\":\"fullscreen\"}"},
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
