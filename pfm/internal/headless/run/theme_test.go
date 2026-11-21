package run

import (
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestArgumentsThemeIsPassedPerRun(t *testing.T) {
	machine := pfmconfig.Config{Claude: pfmconfig.ClaudePrefs{Theme: "from-config"}}
	for _, test := range []struct {
		name     string
		settings map[string]any
		want     string
	}{
		{"no theme passed", nil, `{"cleanupPeriodDays":36500,"env":{"ANTHROPIC_SMALL_FAST_MODEL":"claude-haiku-5-5","CLAUDE_CODE_AUTO_COMPACT_WINDOW":"100000","CLAUDE_CODE_DISABLE_BG_EXIT_HANDOFF":"1","CLAUDE_CODE_DISABLE_FEEDBACK_SURVEY":"1","CLAUDE_CODE_ENABLE_FUNCTION_HOOKS":"1","CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS":"0","CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH":"8","CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION":"9007199254740991","CLAUDE_CODE_NATIVE_CURSOR":"1","CLAUDE_CODE_RETRY_WATCHDOG":"1","CLAUDE_CODE_STOP_HOOK_BLOCK_CAP":"2","CLAUDE_CODE_TMUX_TRUECOLOR":"1","CLAUDE_DISABLE_ADOPT":"1","ENABLE_TOOL_SEARCH":"true"},"outputStyle":"default","showThinkingSummaries":true,"skipAutoPermissionPrompt":true,"skipDangerousModePermissionPrompt":true,"skipWorkflowUsageWarning":true,"tui":"fullscreen"}`},
		{"theme passed", map[string]any{"theme": "t"}, `{"cleanupPeriodDays":36500,"env":{"ANTHROPIC_SMALL_FAST_MODEL":"claude-haiku-5-5","CLAUDE_CODE_AUTO_COMPACT_WINDOW":"100000","CLAUDE_CODE_DISABLE_BG_EXIT_HANDOFF":"1","CLAUDE_CODE_DISABLE_FEEDBACK_SURVEY":"1","CLAUDE_CODE_ENABLE_FUNCTION_HOOKS":"1","CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS":"0","CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH":"8","CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION":"9007199254740991","CLAUDE_CODE_NATIVE_CURSOR":"1","CLAUDE_CODE_RETRY_WATCHDOG":"1","CLAUDE_CODE_STOP_HOOK_BLOCK_CAP":"2","CLAUDE_CODE_TMUX_TRUECOLOR":"1","CLAUDE_DISABLE_ADOPT":"1","ENABLE_TOOL_SEARCH":"true"},"outputStyle":"default","showThinkingSummaries":true,"skipAutoPermissionPrompt":true,"skipDangerousModePermissionPrompt":true,"skipWorkflowUsageWarning":true,"theme":"t","tui":"fullscreen"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			args, err := arguments(Request{Engine: pfmengine.Claude, Config: machine, Settings: test.settings})
			if err != nil {
				t.Fatal(err)
			}
			if !containsPair(args, "--settings", test.want) {
				t.Fatalf("args = %q, want settings %s", args, test.want)
			}
		})
	}
}

func TestArgumentsWithoutAccountStillGetsPassedSettings(t *testing.T) {
	args, err := arguments(Request{
		Engine: pfmengine.Claude, WithoutAccount: true,
		Settings: map[string]any{"theme": "t"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !containsPair(
		args,
		"--settings",
		`{"cleanupPeriodDays":36500,"env":{"ANTHROPIC_SMALL_FAST_MODEL":"claude-haiku-5-5","CLAUDE_CODE_AUTO_COMPACT_WINDOW":"100000","CLAUDE_CODE_DISABLE_BG_EXIT_HANDOFF":"1","CLAUDE_CODE_DISABLE_FEEDBACK_SURVEY":"1","CLAUDE_CODE_ENABLE_FUNCTION_HOOKS":"1","CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS":"0","CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH":"8","CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION":"9007199254740991","CLAUDE_CODE_NATIVE_CURSOR":"1","CLAUDE_CODE_RETRY_WATCHDOG":"1","CLAUDE_CODE_STOP_HOOK_BLOCK_CAP":"2","CLAUDE_CODE_TMUX_TRUECOLOR":"1","CLAUDE_DISABLE_ADOPT":"1","ENABLE_TOOL_SEARCH":"true"},"outputStyle":"default","showThinkingSummaries":true,"skipAutoPermissionPrompt":true,"skipDangerousModePermissionPrompt":true,"skipWorkflowUsageWarning":true,"theme":"t","tui":"fullscreen"}`,
	) {
		t.Fatalf("without-account args = %q", args)
	}
}
