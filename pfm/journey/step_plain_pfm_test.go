//go:build journey

package journey

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// plainPfm: in a plain terminal, in a project of its own, the owner opens the picker and closes it;
// no chat comes of it. Then he opens a shell the way an app launched from inside a chat opens its
// terminals: interactive, asked to open pfm (PFM_AUTO_OPEN=1), carrying the chat's markers (CLAUDECODE,
// its session id, TMUX and TMUX_PANE); pfm says it cleared them and draws the picker all the same, which
// he closes again: none of the markers is left in that shell, which he leaves.
func plainPfm(c *harness.Ctx, s State) State {
	project := c.Unique("plain")
	dir := filepath.Join(s.Home, "work", project)
	term := c.Terminal("plain-pfm")
	term.WaitFor(prompt)
	term.Type("mkdir -p ~/work/" + project + " && cd ~/work/" + project + " && pfm\r")
	term.WaitFor("New chat → " + project)
	term.Press(harness.Esc)
	promptBack(term, "the prompt back under the closed picker")
	term.Type("clear && PFM_AUTO_OPEN=1 CLAUDECODE=1 CLAUDE_CODE_SESSION_ID=" + c.Unique("inherited") +
		" TMUX=/tmp/inherited,1,0 TMUX_PANE=%9 zsh -i\r")
	cleared := "pfm: cleared a chat environment (CLAUDECODE=1) this terminal inherited from its app" +
		" — relaunch the app outside any chat to stop it"
	term.WaitFor("New chat → "+project, stepBound)
	term.Press(harness.Esc)
	promptBack(term, "the inherited shell's prompt under the closed picker")
	if screen := term.Screen(); !strings.Contains(screen, cleared) {
		c.Failf("the inherited shell opened the picker without pfm's line %q:\n%s", cleared, screen)
	}
	markers := "printenv CLAUDECODE CLAUDE_CODE_SESSION_ID TMUX TMUX_PANE"
	if out, errs, code := typedRun(c, term, "inherited-markers", markers); code != 1 || out != "" || errs != "" {
		c.Failf("the inherited shell's %s: exit %d, stdout %q, stderr %q; want exit 1 and nothing printed:"+
			" every chat marker cleared", markers, code, out, errs)
	}
	term.Type("exit\r")
	promptBack(term, "the first shell's prompt")
	for _, line := range strings.Split(read(c, "pfm", "ls", "--tsv"), "\n") {
		if f := strings.Split(line, "\t"); len(f) > 3 && strings.HasPrefix(f[0], "live-") && f[3] == dir {
			c.Failf("closing the picker left a live chat in %s: %s", dir, line)
		}
	}
	return s
}

// promptBack waits for the shell's prompt alone on the terminal's last row.
func promptBack(term *harness.Terminal, what string) {
	term.WaitScreen(what, stepBound, func(screen string) bool {
		lines := strings.Split(screen, "\n")
		return strings.TrimSpace(lines[len(lines)-1]) == prompt
	})
}

// pickerChat: in a fresh plain terminal the owner first reads the login default pfm's install left
// (CLAUDE_CONFIG_DIR and its sentinel name the primary account), then opens the picker in a project of
// his own and walks the new-Claude wizard with Enter. The fake Claude draws in a tmux server pfm minted:
// its window carries pfm's name for it (Claude) with tmux's automatic rename off, there and as the server's
// default; its engine runs on the primary
// account with no sentinel and with Remote Control on, unnamed as the chat is. He renames it from a second
// terminal to a name past tmux's 24-rune window clip: the window holds the clipped spelling, read after
// read, and the terminal title is Claude's own title with its glyph cut, never pfm's window name beside it
// (the fake Claude sets no title: tmux's default, the machine's host name, stands in for it). He types /exit: the terminal, which the chat owned,
// closes, and nothing of the chat is left.
func pickerChat(c *harness.Ctx, s State) State {
	project := c.Unique("picker")
	s.Term, s.Project = "plain-picker", filepath.Join(s.Home, "work", project)
	term := c.Terminal(s.Term)
	term.WaitFor(prompt)
	c.Cleanup(func() { stopPlainChat(c, &s, term) })
	account := s.Home + "/.cc/1"
	out, errs, code := typedRun(
		c,
		term,
		"login-default",
		"printenv CLAUDE_CONFIG_DIR && printenv PFM_CLAUDE_CONFIG_DIR_DEFAULT",
	)
	if want := account + "\n" + account + "\n"; code != 0 || out != want || errs != "" {
		c.Failf("a fresh terminal's CLAUDE_CONFIG_DIR and PFM_CLAUDE_CONFIG_DIR_DEFAULT: exit %d, stdout %q,"+
			" stderr %q; want exit 0, stdout %q", code, out, errs, want)
	}
	term.Type("clear && mkdir -p ~/work/" + project + " && cd ~/work/" + project + " && pfm\r")
	term.WaitFor("New chat → "+project, 20*time.Second)
	newChatSelected(term)
	term.Press(harness.Enter)
	pickEngine(term, "Claude")
	term.Press(harness.Enter)
	term.WaitFor("choose the cache")
	term.Press(harness.Enter)
	term.WaitFor("❯", 20*time.Second)
	s = plainClaudeLive(c, s, attached(c, term))
	s.Name = lsName(c, s)
	server := seatServer(c, s)
	window := tmuxAt(c, server, "display", "-p", "#{window_name}|#{automatic-rename}")
	// An unnamed chat's window carries pfm's engine word until it is named, never the engine's own title.
	if want := "Claude|0"; window != want {
		c.Failf("the picker chat's tmux window (name|automatic-rename) reads %q, want %q", window, want)
	}
	// The server's own default, which every window it opens later inherits, not this window's alone.
	if global := tmuxAt(c, server, "show-options", "-gwv", "automatic-rename"); global != "off" {
		c.Failf("the picker chat's tmux server's global automatic-rename reads %q, want %q", global, "off")
	}
	engineLaunch(c, s, account)
	long := c.Unique("thirty-runes-long-chat-name")
	long = long[:30]
	named := read(c, "pfm", "chat", "name", s.Chat.Session, long)
	if want := "named " + s.Chat.Session + " -> " + long + "\n"; named != want {
		c.Failf("pfm chat name %s %s printed %q, want %q", s.Chat.Session, long, named, want)
	}
	for n := range 3 {
		if n > 0 {
			time.Sleep(2 * time.Second) // the statusline's own window writer runs every 2 s: watch it run
		}
		got := tmuxAt(c, server, "display", "-p", "#{window_name}")
		title := tmuxAt(c, server, "display", "-p", "#{T:set-titles-string}")
		own := tmuxAt(c, server, "display", "-p", "#{pane_title}")
		if _, rest, glyph := strings.Cut(own, " "); glyph {
			own = rest
		}
		if got != long[:24] || title != "⬢ "+own {
			c.Failf("read %d after the rename: window name %q, terminal title %q; want the window %q and the"+
				" title %q, Claude's own", n+1, got, title, long[:24], "⬢ "+own)
		}
	}
	socket := plainSocket(c, s)
	// plainGone's "terminal closed" reads the terminal's shell in the process table: see it there first.
	if _, found := processes(c)[term.Pid()]; !found {
		c.Failf("terminal %s's shell %d is missing from the process table before /exit", term.Name(), term.Pid())
	}
	term.Type("/exit\r")
	term.WaitGone("❯", 20*time.Second)
	plainGone(c, s, term, socket)
	return s
}

// engineLaunch requires the branch's Claude engine to run on account's config dir with no login-default
// sentinel left in its environment, and with Remote Control on, once and with no name: the chat has none yet.
func engineLaunch(c *harness.Ctx, s State, account string) {
	env, err := procTable(c).Environ(s.Chat.Engine)
	if err != nil {
		c.Failf("read engine %d's environment: %v", s.Chat.Engine, err)
	}
	sentinel, carried := env["PFM_CLAUDE_CONFIG_DIR_DEFAULT"]
	if env["CLAUDE_CONFIG_DIR"] != account || carried {
		c.Failf("engine %d runs with CLAUDE_CONFIG_DIR=%q and the login default's sentinel %q (set %t);"+
			" want %q and no sentinel", s.Chat.Engine, env["CLAUDE_CONFIG_DIR"], sentinel, carried, account)
	}
	argv, err := procTable(c).Cmdline(s.Chat.Engine)
	if err != nil {
		c.Failf("read engine %d's argv: %v", s.Chat.Engine, err)
	}
	var remote []string
	for i, word := range argv {
		if key, _, inline := strings.Cut(word, "="); key == "--remote-control" {
			if !inline && i+1 < len(argv) && !strings.HasPrefix(argv[i+1], "-") {
				word += " " + argv[i+1]
			}
			remote = append(remote, word)
		}
	}
	if len(remote) != 1 || remote[0] != "--remote-control" {
		c.Failf(
			"engine %d carries Remote Control as %q, want one bare --remote-control: argv %q",
			s.Chat.Engine,
			remote,
			argv,
		)
	}
}

// seatServer is the tmux socket path of the branch's chat's live seat.
func seatServer(c *harness.Ctx, s State) string {
	r := stateSQL(c,
		"select tmux_socket from seat where state = 'live' and session_id = '"+
			s.Chat.Session+"'")
	if lines := strings.Fields(r); len(lines) == 1 {
		return lines[0]
	}
	c.Failf("chat %s has live tmux seats %q, want one", s.Chat.Session, r)
	return ""
}

// tmuxAt runs tmux against the server at socket path and returns its stdout, trimmed.
func tmuxAt(c *harness.Ctx, server string, args ...string) string {
	return strings.TrimSpace(read(c, append([]string{"tmux", "-S", server}, args...)...))
}

// chatEnded waits for no live row, live seat or engine of session to be left.
func chatEnded(c *harness.Ctx, session string, within time.Duration) {
	c.Poll("chat "+session+" gone", within, func() (bool, string) {
		rows, _ := liveChats(c, "")
		seats := seatsWhere(c, "state = 'live' and session_id = '"+session+"'")
		var left []int
		for pid, id := range engines(processes(c)) {
			if id == session {
				left = append(left, pid)
			}
		}
		_, listed := rows[session]
		return !listed && len(seats) == 0 && len(left) == 0,
			fmt.Sprintf("listed %t, seats %v, engines %v", listed, seats, left)
	})
}
