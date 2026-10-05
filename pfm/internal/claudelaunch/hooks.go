package claudelaunch

import "path/filepath"

const (
	HookEventUserPromptSubmit = "UserPromptSubmit"
	HookExploreMatcher        = "Agent|Task"
	HookRRDirMatcher          = "rr|rr-pro|rr-pro-max"
	// HookResumeMatcher selects the SessionStart source Claude reports for
	// --resume, --continue and the in-app /resume.
	HookResumeMatcher   = "resume"
	hookEventPreToolUse = "PreToolUse"
)

type Hook struct {
	Event, Matcher, Command, Name string
	Async                         bool
}

func HookTemplates(home string) []Hook {
	binary := filepath.Join(home, ".local", "bin", "pfm")
	return []Hook{
		{Event: "SessionStart", Command: binary + " internal launcher-repair", Name: "launcher-repair"},
		{
			Event:   "SessionStart",
			Matcher: HookResumeMatcher,
			Command: binary + " internal resume-unkill",
			Name:    "resume-unkill",
		},
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
}

func StatusLineCommand(home string) string {
	return filepath.Join(home, ".local", "bin", "pfm-statusline")
}

func SubagentStatusLineCommand(home string) string { return StatusLineCommand(home) + " --subagents" }
