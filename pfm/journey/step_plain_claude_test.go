//go:build journey

package journey

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// plainClaude: in a plain terminal the owner goes to the demo project and types claude; the fake
// Claude's box comes up inside the tmux server pfm minted for the chat, which pfm lists as the one
// live Claude chat on that socket, on a live tmux seat, run by one engine. When the branch's subtree
// ends, failed or not, the chat is ended and no engine of it is left.
func plainClaude(c *harness.Ctx, s State) State {
	s.Term, s.Project = "plain-claude", filepath.Join(s.Home, "work", "demo")
	term := c.Terminal(s.Term)
	term.WaitFor(prompt)
	c.Cleanup(func() { stopPlainChat(c, &s, term) })
	term.Type("cd $HOME/work/demo && claude\r")
	term.WaitFor("❯", 20*time.Second)
	return plainClaudeLive(c, s, attached(c, term))
}

// plainReloadTyped: the owner types /reload in the plain chat; the chat comes back in the same
// terminal, on the same socket, seat and session, under a new engine, and draws its status line. The
// step ends once pfm's reload run has exited: an /exit typed while it still runs is swallowed.
func plainReloadTyped(c *harness.Ctx, s State) State {
	term := c.Terminal(s.Term)
	before, socket := s.Chat, plainSocket(c, s)
	term.Type("/reload\r")
	c.Poll("a new engine for session "+s.Chat.Session, 20*time.Second, func() (bool, string) {
		for pid, session := range engines(processes(c)) {
			if session == s.Chat.Session && pid != before.Engine {
				return true, ""
			}
		}
		return false, fmt.Sprintf("engines %v", engines(processes(c)))
	})
	c.Poll("pfm's reload run on "+socket+" done", 20*time.Second, func() (bool, string) {
		for _, p := range processes(c) {
			if strings.Contains(p.Args, " internal reload-run ") && strings.Contains(p.Args, "/"+socket) {
				return false, fmt.Sprintf("pid %d: %s", p.Pid, p.Args)
			}
		}
		return true, ""
	})
	term.WaitGone("/reload", 20*time.Second)
	plainStatusRow(c, s)
	s = plainClaudeLive(c, s, socket)
	if s.Chat.Seat != before.Seat || s.Chat.Engine == before.Engine {
		c.Failf("after the reload chat %s is on seat %s with engine %d, want seat %s and an engine other than %d",
			s.Chat.Session, s.Chat.Seat, s.Chat.Engine, before.Seat, before.Engine)
	}
	return s
}

// plainEnd: the owner types /exit in the plain chat; the box leaves the screen and the terminal, which
// the chat owned, closes. No live row, live seat, engine or tmux process of the chat is left.
func plainEnd(c *harness.Ctx, s State) State {
	term := c.Terminal(s.Term)
	socket := plainSocket(c, s)
	term.Type("/exit\r")
	term.WaitGone("❯", 20*time.Second)
	plainGone(c, s, term, socket)
	return s
}

// plainResumeFromCLI: from a new plain terminal the owner types `pfm chat open {session}`; the ended
// chat comes back in a tmux server of its own, the one live row there on a live seat, run by a Claude
// engine resuming the old session. pfm reads it idle, and its history, read in the project as the
// owner reads it, still holds the earlier turn. When the step's subtree ends, failed or not, the
// resumed chat is ended and no engine of it is left.
func plainResumeFromCLI(c *harness.Ctx, s State) State {
	old := s.Chat
	s.Term = "plain-claude-resume"
	term := c.Terminal(s.Term)
	term.WaitFor(prompt)
	c.Cleanup(func() { stopPlainChat(c, &s, term) })
	term.Type("pfm chat open " + s.Chat.Session + "\r")
	term.WaitFor("❯", 20*time.Second)
	s = plainClaudeLive(c, s, attached(c, term))
	if s.Chat.Seat == old.Seat {
		c.Failf("the resumed chat %s is on the ended chat's seat %s, want a seat of its own", s.Chat.Session, old.Seat)
	}
	if args := processes(c)[s.Chat.Engine].Args; !strings.Contains(args, "--resume "+s.Chat.Session) {
		c.Failf("the resumed engine %d runs %q, want --resume %s", s.Chat.Engine, args, s.Chat.Session)
	}
	c.Poll("chat "+s.Chat.Session+" idle in pfm's status", stepBound, func() (bool, string) {
		state := chatAttention(c, s)
		return state == "idle", state
	})
	history := c.Read("sh", "-c", `cd "$1" && pfm chat history "$2"`, "sh", s.Project, s.Chat.Session)
	turns := historyTurn.FindAllStringSubmatch(history.Stdout, -1)
	if history.Code != 0 || len(turns) < 2 {
		c.Failf("pfm chat history %s exited %d with turns %q, want the earlier turn; stdout %q stderr %q",
			s.Chat.Session, history.Code, turns, history.Stdout, history.Stderr)
	}
	asked, answered := turns[len(turns)-2], turns[len(turns)-1]
	sent := regexp.MustCompile(`^a turn mock-engine: \{"type":"turn","reply":"([^"]+)"\}$`).FindStringSubmatch(asked[2])
	if asked[1] != "user" || sent == nil || answered[1] != "assistant" || answered[2] != sent[1] {
		c.Failf("pfm chat history %s ends %q then %q, want the typed turn prompt then its reply", s.Chat.Session,
			asked[1:], answered[1:])
	}
	return s
}

// plainStatusRow waits for the chat's status line under its box: the account's medal, the model,
// the project and its git branch, then the fleet's counts, which every branch's chats move, so only
// their shape is pinned; an ERR, fleet! or git! segment or a blank row never matches.
func plainStatusRow(c *harness.Ctx, s State) {
	want := regexp.MustCompile(`^🥇 ◇ Sonnet·effort unknown │ ` + regexp.QuoteMeta(filepath.Base(s.Project)) +
		` │ 🌿 master │` + fleetCounts)
	c.Terminal(s.Term).WaitScreen("the status line "+want.String()+" under the box", stepBound,
		func(screen string) bool {
			row, found := statusRow(screen)
			return found && want.MatchString(row)
		})
}

// tmuxClient matches the socket a tmux client names: `-L {name}` or `-S {path}`.
var tmuxClient = regexp.MustCompile(`(?:^|/)tmux (?:-L (\S+)|-S (\S+))`)

// attached is the tmux socket name the terminal's own tmux client is attached to: the one tmux
// process under the terminal's shell. Every chat a plain terminal opens is found this way, never by a
// machine-wide count, which other branches change.
func attached(c *harness.Ctx, term *harness.Terminal) string {
	socket := ""
	c.Poll("the tmux client in terminal "+term.Name(), stepBound, func() (bool, string) {
		table := processes(c)
		var clients []string
		for pid, p := range table {
			if m := tmuxClient.FindStringSubmatch(p.Args); m != nil && under(table, pid, term.Pid()) {
				clients = append(clients, filepath.Base(m[1]+m[2]))
			}
		}
		if len(clients) == 1 {
			socket = clients[0]
		}
		return len(clients) == 1, fmt.Sprintf("tmux clients %v", clients)
	})
	return socket
}

// plainClaudeLive waits for the branch's Claude chat to be the one live row on socket, on a live
// tmux seat there, run by exactly one engine, and returns s with that chat.
func plainClaudeLive(c *harness.Ctx, s State, socket string) State {
	c.Poll("one live Claude chat on tmux socket "+socket+" with its live seat and one engine", stepBound,
		func() (bool, string) {
			rows, listed := liveChats(c, socket)
			seats := seatsWhere(c, "state = 'live' and backend = 'tmux' and tmux_socket like '%/"+socket+"'")
			var session string
			for id, row := range rows {
				session = id
				if row[0] != "live-claude" {
					session = ""
				}
			}
			var pids []int
			for pid, id := range engines(processes(c)) {
				if id != "" && id == session {
					pids = append(pids, pid)
				}
			}
			all := c.Read("sqlite3", stateDB(), "select seat_id, session_id, state, backend, tmux_socket, tmux_pane"+
				" from seat where tmux_socket like '%"+socket+"' or session_id = '"+session+"'")
			var tsv []string
			for _, line := range strings.Split(c.Read("pfm", "ls", "--tsv").Stdout, "\n") {
				if strings.Contains(line, socket) || s.Chat.Session != "" && strings.Contains(line, s.Chat.Session) {
					tsv = append(tsv, line)
				}
			}
			seen := fmt.Sprintf("chats %v (listed %v), live seats %v, engines %v; seats on the socket: %s; "+
				"its pfm ls rows: %q", rows, listed, seats, pids, all.Stdout+all.Stderr, tsv)
			seat, seated := seats[session]
			if len(listed) != 1 || session == "" || len(seats) != 1 || !seated || len(pids) != 1 ||
				s.Chat.Session != "" && session != s.Chat.Session {
				return false, seen
			}
			s.Chat = Chat{Session: session, Seat: seat[0], Engine: pids[0]}
			return true, seen
		})
	return s
}

// plainSocket is the tmux socket name of the branch's chat's live seat.
func plainSocket(c *harness.Ctx, s State) string {
	r := read(c, "sqlite3", stateDB(), "select tmux_socket from seat where state = 'live' and session_id = '"+
		s.Chat.Session+"'")
	if lines := strings.Fields(r); len(lines) == 1 {
		return filepath.Base(lines[0])
	}
	c.Failf("chat %s has live tmux seats %q, want one", s.Chat.Session, r)
	return ""
}

// plainGone waits for the chat to leave every list and process: no live row or seat of its session,
// no engine, no tmux process on socket, and the terminal's shell gone with it.
func plainGone(c *harness.Ctx, s State, term *harness.Terminal, socket string) {
	c.Poll("chat "+s.Chat.Session+" and tmux socket "+socket+" gone", stepBound, func() (bool, string) {
		rows, _ := liveChats(c, "")
		seats := seatsWhere(c, "state = 'live' and session_id = '"+s.Chat.Session+"'")
		var left []string
		for pid, p := range processes(c) {
			if p.Session == s.Chat.Session || strings.Contains(p.Args, socket) || pid == term.Pid() {
				left = append(left, fmt.Sprintf("%d %s", pid, p.Args))
			}
		}
		_, listed := rows[s.Chat.Session]
		return !listed && len(seats) == 0 && len(left) == 0,
			fmt.Sprintf("listed %t, seats %v, processes %v", listed, seats, left)
	})
}

// stopPlainChat ends what the branch started with the owner's own command: `pfm chat end` for its
// chat while a live seat holds it, or, for a chat never identified, the tmux server the terminal
// attached; then it waits for no engine of it to be left. A failure is logged: the joins judge it.
func stopPlainChat(c *harness.Ctx, s *State, term *harness.Terminal) {
	run := func(argv ...string) {
		if r := c.Read(argv...); r.Code != 0 {
			c.Logf("teardown of %s: %q exited %d: %s%s", term.Name(), argv, r.Code, r.Stderr, r.Stdout)
		}
	}
	if s.Chat.Session == "" {
		table := processes(c)
		for pid, p := range table {
			m := tmuxClient.FindStringSubmatch(p.Args)
			if len(m) != 3 || !under(table, pid, term.Pid()) {
				continue
			}
			// Ended through pfm so the chat's seat goes with its server;
			// a server pfm does not know is killed by hand.
			name := m[1]
			if name == "" {
				name = filepath.Base(m[2])
			}
			if r := c.Read("pfm", "chat", "end", name); r.Code == 0 {
				continue
			}
			if m[1] != "" {
				run("tmux", "-L", m[1], "kill-server")
			} else {
				run("tmux", "-S", m[2], "kill-server")
			}
		}
		return
	}
	if rows, _ := liveChats(c, ""); rows[s.Chat.Session] != [2]string{} {
		run("pfm", "chat", "end", s.Chat.Session)
	}
	phase(c, func() {
		c.Poll("no engine of chat "+s.Chat.Session+" left", 20*time.Second, func() (bool, string) {
			var left []string
			for pid, session := range engines(processes(c)) {
				if session == s.Chat.Session {
					left = append(left, fmt.Sprint(pid))
				}
			}
			return len(left) == 0, fmt.Sprintf("%v", left)
		})
	})
}
