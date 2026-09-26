package installer

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
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
