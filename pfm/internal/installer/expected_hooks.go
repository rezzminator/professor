package installer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

const (
	hookEventUserPromptSubmit = "UserPromptSubmit"
	hookExploreMatcher        = "Agent|Task"
	// hookRRDirMatcher names the agents whose prompt reads the RR-DIR line:
	// templates/global/agents/rr.md and the variants declared from it.
	hookRRDirMatcher = "rr|super-rr"
	// hookMatchAll is the matcher that fires for every tool or agent name.
	hookMatchAll = "*"
	// stateBroken is the VS Code extension link state (vscode_index.go); the
	// hook check has its own states in hook_probe.go.
	stateBroken = "broken"
)

// ExpectedHook is one hook the installer converges and owns.
type ExpectedHook struct {
	Target  string
	File    string
	Event   string
	Matcher string
	Command string
	Name    string
	// Async marks a hook the harness runs in the background: the settings
	// writer puts "async": true in its hook object and converges an owned
	// entry that lacks it. It is not part of the ownership key.
	Async bool
}

// ExpectedHooks is the installer's exported source of truth for doctor. The
// installer writes configured account settings only; it never writes a
// project-level .claude/settings.json or invents an engine account.
func ExpectedHooks(home string, config pfmconfig.Config) []ExpectedHook {
	type target struct {
		name string
		file string
	}
	targets := make([]target, 0, len(config.Accounts))
	for _, account := range config.Accounts {
		targets = append(targets, target{
			name: fmt.Sprintf("claude[%d]", account.ID),
			file: filepath.Join(account.ConfigDir, "settings.json"),
		})
	}
	seen := map[string]bool{}
	templates := claudeHookTemplates(home)
	result := make([]ExpectedHook, 0, len(targets)*len(templates))
	for _, target := range targets {
		physical := physicalSettingsPath(target.file)
		if seen[physical] {
			continue
		}
		seen[physical] = true
		for _, hook := range templates {
			hook.Target = target.name
			hook.File = target.file
			result = append(result, hook)
		}
	}
	// Codex accounts carry no expected hook: the fleet prompt reaches a Codex
	// session through config.toml's developer_instructions, and the
	// SessionStart appendix hook pfm used to own is retired.
	return result
}

func claudeHookTemplates(home string) []ExpectedHook {
	binary := filepath.Join(home, ".local", "bin", "pfm")
	templates := []ExpectedHook{
		{Event: "SessionStart", Command: binary + " internal launcher-repair", Name: "launcher-repair"},
		{Event: hookEventUserPromptSubmit, Command: binary + " usage-hook", Name: "usage"},
		{Event: "SessionEnd", Command: binary + " internal clear-kill", Name: "clear-kill"},
		{Event: "SessionEnd", Command: binary + " internal exit-close", Name: "exit-close"},
		{
			Event:   "PreToolUse",
			Matcher: hookExploreMatcher,
			Command: binary + " internal explore-deny",
			Name:    "explore-deny",
		},
		{
			// Every Bash call in every repository: only gitter writes shared
			// git state (docs/design/hooks/hooks.md § git-guard).
			Event:   "PreToolUse",
			Matcher: "Bash",
			Command: binary + " internal git-guard",
			Name:    "git-guard",
		},
		{
			Event:   "SubagentStart",
			Matcher: hookRRDirMatcher,
			Command: binary + " internal rr-dir",
			Name:    "rr-dir",
		},
		{Event: hookEventUserPromptSubmit, Command: binary + " internal epic-inject", Name: "epic-inject"},
		{Event: hookEventUserPromptSubmit, Command: binary + " internal reload-intercept", Name: "reload-intercept"},
		{Event: hookEventUserPromptSubmit, Command: binary + " internal exit-intercept", Name: "exit-intercept"},
		{Event: hookEventUserPromptSubmit, Command: binary + " internal compact-nudge", Name: "compact-nudge"},
	}
	// callmeter is one command registered on seven events
	// (docs/design/hooks/callmeter.md § The hooks): PreToolUse matches Bash
	// only (the directory a command starts in), the other tool events and the
	// subagent events match every name, PostToolBatch and Stop carry no name
	// to match. Every registration runs async so the hook never sits in front
	// of a call.
	callmeter := binary + " internal callmeter"
	for _, placement := range []struct{ event, matcher string }{
		{"PreToolUse", "Bash"},
		{"PostToolUse", hookMatchAll},
		{"PostToolUseFailure", hookMatchAll},
		{"PostToolBatch", ""},
		{"SubagentStart", hookMatchAll},
		{"SubagentStop", hookMatchAll},
		{"Stop", ""},
	} {
		templates = append(templates, ExpectedHook{
			Event: placement.event, Matcher: placement.matcher,
			Command: callmeter, Name: "callmeter", Async: true,
		})
	}
	return templates
}

func commandByName(hooks []ExpectedHook, name string) string {
	for _, hook := range hooks {
		if hook.Name == name {
			return hook.Command
		}
	}
	panic("installer expected hook is missing: " + name)
}

func physicalSettingsPath(path string) string {
	candidate := filepath.Clean(path)
	var missing []string
	for {
		physical, err := filepath.EvalSymlinks(candidate)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				physical = filepath.Join(physical, missing[i])
			}
			return filepath.Clean(physical)
		}
		parent := filepath.Dir(candidate)
		if !errors.Is(err, os.ErrNotExist) || parent == candidate {
			return filepath.Clean(path)
		}
		missing = append(missing, filepath.Base(candidate))
		candidate = parent
	}
}
