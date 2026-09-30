package claudelaunch

import "path/filepath"

const (
	HookEventUserPromptSubmit = "UserPromptSubmit"
	HookExploreMatcher        = "Agent|Task"
	HookRRDirMatcher          = "rr|super-rr|heavy-rr"
	HookMatchAll              = "*"
	hookEventPreToolUse       = "PreToolUse"
)

type Hook struct {
	Event, Matcher, Command, Name string
	Async                         bool
}

func HookTemplates(home string) []Hook {
	binary := filepath.Join(home, ".local", "bin", "pfm")
	templates := []Hook{
		{Event: "SessionStart", Command: binary + " internal launcher-repair", Name: "launcher-repair"},
		{Event: HookEventUserPromptSubmit, Command: binary + " usage-hook", Name: "usage"},
		{Event: "SessionEnd", Command: binary + " internal clear-kill", Name: "clear-kill"},
		{Event: "SessionEnd", Command: binary + " internal exit-close", Name: "exit-close"},
		{
			Event:   hookEventPreToolUse,
			Matcher: HookExploreMatcher,
			Command: binary + " internal explore-deny",
			Name:    "explore-deny",
		},
		{Event: hookEventPreToolUse, Matcher: "Bash", Command: binary + " internal git-guard", Name: "git-guard"},
		{Event: "SubagentStart", Matcher: HookRRDirMatcher, Command: binary + " internal rr-dir", Name: "rr-dir"},
		{Event: HookEventUserPromptSubmit, Command: binary + " internal epic-inject", Name: "epic-inject"},
		{Event: HookEventUserPromptSubmit, Command: binary + " internal reload-intercept", Name: "reload-intercept"},
		{Event: HookEventUserPromptSubmit, Command: binary + " internal exit-intercept", Name: "exit-intercept"},
	}
	for _, placement := range []struct{ event, matcher string }{
		{hookEventPreToolUse, "Bash"},
		{"PostToolUse", HookMatchAll},
		{"PostToolUseFailure", HookMatchAll},
		{"PostToolBatch", ""},
		{"SubagentStart", HookMatchAll},
		{"SubagentStop", HookMatchAll},
		{"Stop", ""},
	} {
		templates = append(templates, Hook{
			Event: placement.event, Matcher: placement.matcher,
			Command: binary + " internal callmeter", Name: "callmeter", Async: true,
		})
	}
	return templates
}

func StatusLineCommand(home string) string {
	return filepath.Join(home, ".local", "bin", "pfm-statusline")
}

func SubagentStatusLineCommand(home string) string { return StatusLineCommand(home) + " --subagents" }
