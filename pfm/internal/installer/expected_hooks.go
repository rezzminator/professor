package installer

import (
	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// stateBroken is the VS Code extension link state (vscode_index.go).
const stateBroken = "broken"

// ExpectedHook describes a launch template or a Codex hook probe result.
type ExpectedHook struct {
	Target  string
	File    string
	Event   string
	Matcher string
	Command string
	Name    string
	// Async marks a hook the launch harness runs in the background.
	Async bool
}

func claudeHookTemplates(home string) []ExpectedHook {
	registrations := claudelaunch.HookTemplates(home)
	templates := make([]ExpectedHook, 0, len(registrations))
	for _, hook := range registrations {
		templates = append(templates, ExpectedHook{
			Event: hook.Event, Matcher: hook.Matcher,
			Command: hook.Command, Name: hook.Name, Async: hook.Async,
		})
	}
	return templates
}

func physicalSettingsPath(path string) string {
	return paths.PhysicalPath(path)
}
