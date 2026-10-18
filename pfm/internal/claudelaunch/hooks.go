package claudelaunch

import (
	"path/filepath"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/herdr"
)

const (
	HookEventUserPromptSubmit = "UserPromptSubmit"
	HookRRDirMatcher          = "rr|rr-pro|rr-pro-max|gpt-rr|gpt-rr-pro|gpt-rr-pro-max"
	// HookHerdrNotificationMatcher selects the Notification types the Herdr
	// reporter maps (blocked on a permission or elicitation dialog, idle on
	// idle_prompt); every other notification spawns nothing.
	HookHerdrNotificationMatcher = "permission_prompt|elicitation_dialog|idle_prompt"
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
		{Event: hookEventPreToolUse, Matcher: "Bash", Command: binary + " internal git-guard", Name: "git-guard"},
		{Event: hookEventPreToolUse, Matcher: "Bash", Command: binary + " internal store-guard", Name: "store-guard"},
		{Event: "SubagentStart", Matcher: HookRRDirMatcher, Command: binary + " internal rr-dir", Name: "rr-dir"},
		{Event: HookEventUserPromptSubmit, Command: binary + " internal epic-inject", Name: "epic-inject"},
		{Event: HookEventUserPromptSubmit, Command: binary + " internal reload-intercept", Name: "reload-intercept"},
		{Event: HookEventUserPromptSubmit, Command: binary + " internal exit-intercept", Name: "exit-intercept"},
		{Event: "SessionStart", Command: herdrCommand(binary, herdr.EventStart), Name: "herdr-start"},
		{
			Event:   HookEventUserPromptSubmit,
			Command: herdrCommand(binary, herdr.EventWorking),
			Name:    "herdr-working-prompt",
			Async:   true,
		},
		{
			Event:   "PostToolUse",
			Command: herdrCommand(binary, herdr.EventWorking),
			Name:    "herdr-working-tool",
			Async:   true,
		},
		{
			// The tool a permission dialog asked about can fail once approved: its
			// failure leaves the block as its success does (hookentry/herdr_hold.go).
			Event:   "PostToolUseFailure",
			Command: herdrCommand(binary, herdr.EventWorking),
			Name:    "herdr-working-tool-failure",
			Async:   true,
		},
		{
			Event:   "PermissionRequest",
			Command: herdrCommand(binary, herdr.EventBlocked),
			Name:    "herdr-blocked",
			Async:   true,
		},
		{
			Event:   "Notification",
			Matcher: HookHerdrNotificationMatcher,
			Command: herdrCommand(binary, herdr.EventNotification),
			Name:    "herdr-notification",
			Async:   true,
		},
		{Event: "Stop", Command: herdrCommand(binary, herdr.EventIdle), Name: "herdr-idle"},
		{Event: "SessionEnd", Command: herdrCommand(binary, herdr.EventEnd), Name: "herdr-end"},
	}
}

// herdrCommand is the Claude row of the Herdr reporter hook; every account
// gets it from the launch-time settings, with no hand edit of settings.json.
func herdrCommand(binary string, event herdr.Event) string {
	return herdr.HookCommand(binary, pfmengine.Claude, event)
}

func StatusLineCommand(home string) string {
	return filepath.Join(home, ".local", "bin", "pfm-statusline")
}

func SubagentStatusLineCommand(home string) string { return StatusLineCommand(home) + " --subagents" }
