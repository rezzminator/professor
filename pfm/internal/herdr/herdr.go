// Package herdr is pfm's reporter for the Herdr terminal multiplexer
// (https://herdr.dev): a pfm-launched chat of any engine reports its state,
// its session and its resume command to the Herdr pane it is attached in, and
// releases the pane when it ends. It follows Herdr's reporter contract
// (add-herdr-support): a stable source that never starts with `herdr:`, a
// strictly increasing seq, a resume_argv only while the source holds the pane,
// and a release on exit. Outside a Herdr pane nothing here runs.
package herdr

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// Source is the one integration identity pfm reports under. Herdr's own
// integrations use the `herdr:` prefix; pfm must never start with it.
const Source = "pfm"

// ReportDeadline bounds one reporter run, from its first tmux call through its
// error log. EngineKillTimeout is the kill timer an engine (Codex's handler
// timeout, the OpenCode plugin's) sets on a reporter: past the deadline by a
// margin for the reporter's start-up and its payload read, so a reporter that
// ran out of time still logs its error before the kill.
const (
	ReportDeadline    = 3 * time.Second
	EngineKillTimeout = 5 * time.Second
)

// Herdr's environment contract: a process inside a Herdr pane carries these.
const (
	EnvMarker = "HERDR_ENV"
	EnvPane   = "HERDR_PANE_ID"
	EnvSocket = "HERDR_SOCKET_PATH"
	EnvBin    = "HERDR_BIN_PATH"
)

// The tmux names a reporter hook resolves its pane through.
const (
	EnvTmux     = "TMUX"
	EnvTmuxPane = "TMUX_PANE"
)

// Env lists the Herdr variables a pfm chat tmux server copies from each
// attaching client into the session environment (tmux update-environment), so
// the reporter finds the pane of the CURRENT attach, never the one that
// created the server.
func Env() []string { return []string{EnvMarker, EnvPane, EnvSocket, EnvBin} }

// StripPane returns environ without the Herdr names: a child that is no chat
// bound to the parent's pane never reports as it.
func StripPane(environ []string) []string { return without(environ, Env()) }

// StripReporter returns environ without the Herdr names and the tmux pane
// names: a child that can fire a reporter hook finds its pane through either.
func StripReporter(environ []string) []string {
	return without(environ, append(Env(), EnvTmux, EnvTmuxPane))
}

func without(environ, names []string) []string {
	kept := make([]string, 0, len(environ))
	for _, entry := range environ {
		if name, _, _ := strings.Cut(entry, "="); !slices.Contains(names, name) {
			kept = append(kept, entry)
		}
	}
	return kept
}

// Event is one verb of `pfm internal herdr-state`.
type Event string

const (
	// EventStart is a session beginning or switching in place: idle, with the
	// session and its resume command.
	EventStart Event = "start"
	// EventWorking is a turn running.
	EventWorking Event = "working"
	// EventBlocked is the agent waiting on the user, with a message.
	EventBlocked Event = "blocked"
	// EventIdle is the agent ready for input.
	EventIdle Event = "idle"
	// EventEnd is the user quitting: the pane is released.
	EventEnd Event = "end"
	// EventNotification carries a Claude Notification payload and maps by its
	// notification_type.
	EventNotification Event = "notification"
	// EventAttach registers the tmux client that just attached from a Herdr
	// pane and re-reports the stored state to that pane.
	EventAttach Event = "attach"
	// EventSync re-binds the registered tmux clients whose Herdr pane moved; a
	// client never registered is left alone, since the session environment it
	// would read holds the Herdr variables of the last client that attached.
	EventSync Event = "sync"
	// EventTitle carries a chat title changed inside OpenCode ({"session_id",
	// "title"}); the reporter renames the pane's window and re-sends the name.
	EventTitle Event = "title"
	// EventDetach releases the pane of a registered tmux client that detached.
	EventDetach Event = "detach"
)

// FiredByTmux reports whether a tmux hook fires the verb: it carries no engine
// payload, and no nested engine can have sent it.
func (event Event) FiredByTmux() bool {
	switch event {
	case EventAttach, EventSync, EventDetach, EventName:
		return true
	}
	return false
}

// Events lists every verb the entry accepts.
func Events() []Event {
	return []Event{
		EventStart, EventWorking, EventBlocked, EventIdle, EventEnd, EventNotification,
		EventAttach, EventSync, EventDetach, EventName, EventTitle,
	}
}

// State is a Herdr agent state.
type State string

const (
	StateIdle    State = "idle"
	StateWorking State = "working"
	StateBlocked State = "blocked"
)

// Label is the agent name Herdr shows for an engine: the engine's own long
// name (claude, codex, opencode).
func Label(id pfmengine.ID) string { return pfmengine.MustLookup(id).LongName }

// HookCommand is the command line every engine's hook runs for one event.
func HookCommand(binary string, id pfmengine.ID, event Event) string {
	return binary + " internal herdr-state --engine " + Label(id) + " " + string(event)
}

// Herdr's resume_argv limits.
const (
	maxResumeArgs  = 64
	maxResumeBytes = 8 << 10
	// ResumeCommand is the first word of the resume command: a plain name on
	// PATH, never a path.
	ResumeCommand = "pfm"
)

// ResumeArgv is the command Herdr runs after a server restart to reopen a
// chat: `pfm chat open {session id}`. It enforces Herdr's rules before the
// request leaves: a plain command word, no apostrophe or control character in
// any word, at most 64 words and 8 KiB.
func ResumeArgv(sessionID string) ([]string, error) {
	argv := []string{ResumeCommand, "chat", "open", sessionID}
	if sessionID == "" {
		return nil, fmt.Errorf("resume_argv: empty session id")
	}
	total := 0
	for _, word := range argv {
		if strings.ContainsRune(word, '\'') {
			return nil, fmt.Errorf("resume_argv: word %q holds an apostrophe, which Herdr refuses", word)
		}
		for _, r := range word {
			if unicode.IsControl(r) {
				return nil, fmt.Errorf("resume_argv: word %q holds control character %U, which Herdr refuses", word, r)
			}
		}
		if !utf8.ValidString(word) {
			return nil, fmt.Errorf("resume_argv: word %q is not valid UTF-8", word)
		}
		total += len(word)
	}
	if len(argv) > maxResumeArgs || total > maxResumeBytes {
		return nil, fmt.Errorf(
			"resume_argv: %d words, %d bytes exceed Herdr's %d words and %d bytes",
			len(argv),
			total,
			maxResumeArgs,
			maxResumeBytes,
		)
	}
	if strings.ContainsRune(argv[0], '/') {
		return nil, fmt.Errorf("resume_argv: first word %q is a path, Herdr wants a plain command name", argv[0])
	}
	return argv, nil
}
