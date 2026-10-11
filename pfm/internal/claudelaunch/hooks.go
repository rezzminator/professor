package claudelaunch

import (
	"path/filepath"
	"strings"
)

const (
	HookEventUserPromptSubmit = "UserPromptSubmit"
	HookExploreMatcher        = "Agent|Task"
	hookTracerIntake          = "tracer-intake"
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
	tracerIntake := tracerIntakeCommand(filepath.Join(home, ".claude", "skills", "tracetree", "tracetree.py"))
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
		{Event: hookEventPreToolUse, Matcher: HookExploreMatcher, Command: tracerIntake, Name: hookTracerIntake},
		{Event: hookEventPreToolUse, Matcher: "Bash", Command: binary + " internal git-guard", Name: "git-guard"},
		{Event: "SubagentStart", Matcher: HookRRDirMatcher, Command: binary + " internal rr-dir", Name: "rr-dir"},
		{Event: HookEventUserPromptSubmit, Command: binary + " internal epic-inject", Name: "epic-inject"},
		{Event: HookEventUserPromptSubmit, Command: binary + " internal reload-intercept", Name: "reload-intercept"},
		{Event: HookEventUserPromptSubmit, Command: binary + " internal exit-intercept", Name: "exit-intercept"},
	}
}

// tracerIntakeCommand runs the tracetree kit's `intake` hook, which opens a
// tracer spawn's run and rewrites its brief. python3 exits 2 on a kit file it
// cannot open, and Claude Code blocks the tool call on exit 2, so any failure
// of the command becomes exit 1, a non-blocking error whose stderr line names
// the hook and the kit: a missing kit or python3 never stops a spawn and never
// reads as silence.
func tracerIntakeCommand(kit string) string {
	failed := "pfm tracer-intake hook failed, the spawn goes on without a run: python3 " + kit + " intake exited "
	report := "rc=$?; printf '%s%s\\n' " + shellQuote(failed) + " \"$rc\" >&2; exit 1"
	return "python3 " + shellQuote(kit) + " intake || { " + report + "; }"
}

// shellQuote is one POSIX single-quoted word. internal/action holds the same
// rule but imports this package.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func StatusLineCommand(home string) string {
	return filepath.Join(home, ".local", "bin", "pfm-statusline")
}

func SubagentStatusLineCommand(home string) string { return StatusLineCommand(home) + " --subagents" }
