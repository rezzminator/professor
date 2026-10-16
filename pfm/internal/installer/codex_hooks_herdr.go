package installer

import (
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/herdr"
)

// codexHerdrTimeoutSeconds bounds a Herdr reporter handler. Codex runs a
// handler synchronously (it does not run `async` handlers), so the cap keeps
// a stuck reporter from holding a turn; it outlasts the reporter's own
// deadline, so a reporter out of time logs its error before Codex kills it,
// and the entry always exits 0.
const codexHerdrTimeoutSeconds = int(herdr.EngineKillTimeout / time.Second)

// codexHerdrHookRows maps each Codex hook event to the reporter verb it runs.
// Codex's SessionEnd releases the pane when the chat exits: Herdr's idle-shell
// net does not release a quit Codex chat. Codex's Interrupt (an Esc at an
// approval, which denies it, or an aborted turn) fires no Stop, so it reports
// idle: the pane leaves blocked at once.
var codexHerdrHookRows = []struct {
	event string
	verb  herdr.Event
}{
	{"SessionStart", herdr.EventStart},
	{"UserPromptSubmit", herdr.EventWorking},
	{"PostToolUse", herdr.EventWorking},
	{"PermissionRequest", herdr.EventBlocked},
	{"Stop", herdr.EventIdle},
	{"Interrupt", herdr.EventIdle},
	{"SessionEnd", herdr.EventEnd},
}

// codexHerdrHooks is the Herdr reporter handler of every Codex hook event a
// pfm-wired hooks.json carries: no matcher (every SessionStart source, every
// tool), the `--engine codex` spelling of herdr.HookCommand.
func codexHerdrHooks(home string) []ExpectedHook {
	binary := home + "/.local/bin/pfm"
	hooks := make([]ExpectedHook, 0, len(codexHerdrHookRows))
	for _, row := range codexHerdrHookRows {
		hooks = append(hooks, ExpectedHook{
			Target:  hookTargetCodex,
			Event:   row.event,
			Command: herdr.HookCommand(binary, pfmengine.Codex, row.verb),
			Name:    "herdr-" + string(row.verb) + "-" + row.event,
		})
	}
	return hooks
}

// ensureCodexHerdrHooks appends every missing Herdr handler to the document
// and reports whether it wrote.
func ensureCodexHerdrHooks(document map[string]any, home string) bool {
	changed := false
	for _, hook := range codexHerdrHooks(home) {
		if ensureCodexHook(document, hook, codexHerdrTimeoutSeconds) {
			changed = true
		}
	}
	return changed
}

// codexOwnedHookKey reports whether key is a hook pfm owns in a Codex
// hooks.json: the resume-unkill handler or one of the Herdr handlers.
func codexOwnedHookKey(key settingsHookKey, resumeUnkill ExpectedHook, home string) bool {
	if key.Event == resumeUnkill.Event && key.Matcher == resumeUnkill.Matcher && key.Command == resumeUnkill.Command {
		return true
	}
	for _, hook := range codexHerdrHooks(home) {
		if key.Event == hook.Event && key.Matcher == hook.Matcher && key.Command == hook.Command {
			return true
		}
	}
	return false
}
