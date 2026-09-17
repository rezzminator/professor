package config

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
func ChatServerOptions(titles *TmuxTitles) [][]string {
	return append(
		TmuxTitlesOrDefault(titles).Options(),
		[]string{"set-window-option", "-g", "automatic-rename", "off"},
	)
}

// Tmux is the fleet-wide tmux posture. It is not per-account: a terminal title
// belongs to the terminal, not to whichever account happens to be in the pane.
type Tmux struct {
	Titles TmuxTitles
}
