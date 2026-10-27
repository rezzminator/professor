package config

import (
	"fmt"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/herdr"
)

// TmuxTitles is the tmux.titles policy: whether pfm owns the OUTER terminal's
// title on a server it creates.
//
// Enabled sets `set-titles on` plus pfm's own set-titles-string, so a terminal
// tab reads the chat's window name. Disabled sets NEITHER option and leaves
// whatever the host put there — a host that emits its own OSC title on the
// outer pty before tmux starts (and keeps `set-titles off` so tmux cannot
// clobber it) keeps its tab badges. The default is enabled, which is what
// every install did before this key existed.
type TmuxTitles struct {
	Enabled bool
}

// DefaultTmuxTitles is the policy when the file says nothing: pfm owns the
// title.
func DefaultTmuxTitles() TmuxTitles {
	return TmuxTitles{Enabled: true}
}

// TmuxTitlesOrDefault resolves an OPTIONAL policy. A nil pointer is the
// default (pfm owns the title), never "off": a tmux client constructed without
// a machine config must keep today's behaviour rather than silently hand the
// terminal title to the host.
func TmuxTitlesOrDefault(titles *TmuxTitles) TmuxTitles {
	if titles == nil {
		return DefaultTmuxTitles()
	}
	return *titles
}

// TmuxTitlesString is the format pfm gives tmux when it owns the title. It is
// stated once here and reaches a server only through ChatServerOptions, so the
// tab of one engine can never stop matching the tab of another.
// It renders ONE name per tab. A Claude pane (a `cc-*` session) shows Claude
// Code's own title with the status glyph stripped: it follows /rename (and
// `pfm chat name`, which injects /rename) at once, unclipped, and holds still
// through the spinner — tmux re-emits only on change, which
// bin/tmux-title-renudge relies on. The window name would trail a rename by a
// statusline redraw and clip at gather.WindowNameRunes. Every other engine
// shows #{window_name}, which name-sync converges: a Codex pane's own title is
// its working directory, not its name.
const TmuxTitlesString = "⬢ #{?#{m:cc-*,#{session_name}},#{s/^[^ ]* //:pane_title},#{window_name}}"

// Options returns the tmux `set-option` argument vectors that put this policy
// on a server. A disabled policy returns none: pfm applies neither option.
func (titles TmuxTitles) Options() [][]string {
	if !titles.Enabled {
		return nil
	}
	return [][]string{
		{"set-option", "-g", "set-titles", "on"},
		{"set-option", "-g", "set-titles-string", TmuxTitlesString},
	}
}

// ChatServerOptions is the ONE option list a chat's tmux server carries:
// spawn.TmuxSpawner.NewSession — the one chat-server creator every door calls —
// applies it at birth, and name-sync converges every live server onto it. The
// title half follows the policy (nil is the default, pfm-owned); automatic-
// rename off is never gated, because the window name is the fleet's DNS
// record and pfm is its only writer.
func ChatServerOptions(titles *TmuxTitles, pfmBinary string) [][]string {
	options := append(
		TmuxTitlesOrDefault(titles).Options(),
		[]string{"set-window-option", "-g", "automatic-rename", "off"},
	)
	return append(options, herdrServerOptions(pfmBinary)...)
}

// Tmux is the fleet-wide tmux posture. It is not per-account: a terminal title
// belongs to the terminal, not to whichever account happens to be in the pane.
type Tmux struct {
	Titles TmuxTitles
}

// herdrServerOptions is the Herdr half of the chat-server option list. The
// chat's own process environment keeps the Herdr variables of whatever shell
// CREATED the server, so a chat attached later (or in another pane) would
// report to the wrong pane. tmux copies update-environment variables from the
// attaching client into the session environment on every attach, and removes
// them when the client lacks them: the reporter reads the session environment
// and so finds the pane of the current attach. Indexed entries keep tmux's
// default list and are idempotent for name-sync. The client-attached hook
// (`attach`) registers the attaching client with its pane and re-reports the
// stored state, so a chat attached late shows at once; the client-resized hook
// (`sync`) re-binds a registered client only, so a chat whose Herdr pane moved
// (the move usually resizes its PTY) re-binds without waiting for its next
// event, while a client that attached without Herdr never takes the pane of
// the last one that attached with it; the client-detached
// hook releases that pane; the window-renamed hook re-sends the chat's name to
// the Herdr panes registered for the session. Dropping herdrResizedHook is the
// one-line way to turn the resize re-bind off.
func herdrServerOptions(pfmBinary string) [][]string {
	options := make([][]string, 0, len(herdr.Env())+1)
	for index, name := range herdr.Env() {
		options = append(
			options,
			[]string{"set-option", "-g", fmt.Sprintf("update-environment[%d]", herdrEnvIndexBase+index), name},
		)
	}
	return append(
		options,
		herdrHook(herdrAttachedHook, pfmBinary, herdr.EventAttach),
		herdrHook(herdrDetachedHook, pfmBinary, herdr.EventDetach),
		herdrHook(herdrResizedHook, pfmBinary, herdr.EventSync),
		herdrHook(herdrRenamedHook, pfmBinary, herdr.EventName),
	)
}

func herdrHook(hook, pfmBinary string, event herdr.Event) []string {
	return []string{"set-hook", "-g", hook, herdrHookCommand(pfmBinary, event)}
}

// herdrHookCommand is the tmux command a client hook runs: `run-shell -b` of
// the pfm binary, every interpolated value quoted for the shell it reaches.
// The binary path is single-quoted for sh (a `#` doubled, since run-shell
// expands formats first); the tmux values go through the `q:` format modifier,
// which backslash-escapes them for sh. The whole shell command is then one
// tmux double-quoted argument, in exactly the spelling tmux prints a stored
// hook back (backslash, double quote and dollar escaped), so name-sync's
// read-back of a converged server compares equal. A client event names the
// client (`--client`); the name event is a window's, which has none.
func herdrHookCommand(pfmBinary string, event herdr.Event) string {
	shell := shellSingleQuote(pfmBinary) + " internal herdr-state " + string(event) +
		" --socket #{q:socket_path} --session #{q:session_name}"
	if event != herdr.EventName {
		shell += " --client #{q:client_pid}"
	}
	return "run-shell -b " + tmuxDoubleQuote(shell)
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(value, "'", `'\''`), "#", "##") + "'"
}

func tmuxDoubleQuote(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`).Replace(value) + `"`
}

const (
	herdrEnvIndexBase = 20
	herdrAttachedHook = "client-attached[50]"
	herdrDetachedHook = "client-detached[50]"
	herdrResizedHook  = "client-resized[50]"
	herdrRenamedHook  = "window-renamed[50]"
)
