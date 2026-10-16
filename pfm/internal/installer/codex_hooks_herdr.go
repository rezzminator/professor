package installer

import (
	"encoding/json"
	"strconv"
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

// codexHerdrCappedTimeoutSeconds is the most Codex allows a SessionEnd or
// Interrupt handler (codex-rs hooks discovery.rs SESSION_END_MAX_TIMEOUT_SEC);
// over it Codex warns at load, which `pfm doctor` reads as an unreadable
// hooks.json.
const codexHerdrCappedTimeoutSeconds = 3

// codexHerdrHookRows maps each Codex hook event to the reporter verb it runs.
// Codex's SessionEnd releases the pane when the chat exits: Herdr's idle-shell
// net does not release a quit Codex chat. Codex's Interrupt (an Esc at an
// approval, which denies it, or an aborted turn) fires no Stop, so it reports
// idle: the pane leaves blocked at once.
var codexHerdrHookRows = []struct {
	event   string
	verb    herdr.Event
	timeout int
}{
	{"SessionStart", herdr.EventStart, codexHerdrTimeoutSeconds},
	{"UserPromptSubmit", herdr.EventWorking, codexHerdrTimeoutSeconds},
	{"PostToolUse", herdr.EventWorking, codexHerdrTimeoutSeconds},
	{"PermissionRequest", herdr.EventBlocked, codexHerdrTimeoutSeconds},
	{"Stop", herdr.EventIdle, codexHerdrTimeoutSeconds},
	{"Interrupt", herdr.EventIdle, codexHerdrCappedTimeoutSeconds},
	{"SessionEnd", herdr.EventEnd, codexHerdrCappedTimeoutSeconds},
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

// ensureCodexHerdrHooks appends every missing Herdr handler to the document,
// sets each present one's timeout to its row's, and reports whether it wrote.
func ensureCodexHerdrHooks(document map[string]any, home string) bool {
	changed := false
	for index, hook := range codexHerdrHooks(home) {
		timeout := codexHerdrHookRows[index].timeout
		if ensureCodexHook(document, hook, timeout) {
			changed = true
		}
		if setCodexHookTimeout(document, hook, timeout) {
			changed = true
		}
	}
	return changed
}

// setCodexHookTimeout sets the timeout of every handler carrying the hook's
// command, in an entry of the hook's event whose matcher is the hook's, to
// timeoutSeconds, and reports whether one differed.
func setCodexHookTimeout(document map[string]any, hook ExpectedHook, timeoutSeconds int) bool {
	changed := false
	for _, entry := range hookEntries(document, hook.Event, false) {
		if matcher, _ := entry["matcher"].(string); matcher != hook.Matcher {
			continue
		}
		handlers, _ := entry["hooks"].([]any)
		for _, handlerValue := range handlers {
			handler, _ := handlerValue.(map[string]any)
			if command, _ := handler[configCommandKey].(string); command != hook.Command {
				continue
			}
			if !codexTimeoutIs(handler["timeout"], timeoutSeconds) {
				handler["timeout"] = timeoutSeconds
				changed = true
			}
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

// codexTimeoutIs reports whether a handler's timeout is the number seconds: a
// decoded json.Number, or the int an install just set. A string, a fraction or
// an absent timeout is not, so it is rewritten.
func codexTimeoutIs(value any, seconds int) bool {
	switch value := value.(type) {
	case json.Number:
		return value.String() == strconv.Itoa(seconds)
	case int:
		return value == seconds
	case float64:
		return value == float64(seconds)
	}
	return false
}
