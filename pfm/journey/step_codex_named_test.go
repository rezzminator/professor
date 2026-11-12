//go:build journey

package journey

import (
	"fmt"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// codexNamed: in the pane its ended Codex chat left, the owner leaves the picker for the shell and types
// `pfm chat new --engine cx --name {name}`; Herdr opens a tab for it and pfm lists one live Codex chat of
// that name. Then a thread title arrives in that Codex (the fake writes Codex's session_index row, as
// Codex does for a titled thread): its status line shows the title, the name sync that row fires runs,
// and pfm still lists the chat under the owner's --name (escape chat-birth/new-from-cli, Codex name).
func codexNamed(c *harness.Ctx, s State) State {
	s = chatNewInPane(c, s, "cx", "live-codex")
	term := c.Terminal(s.Term)
	title := c.Unique("title")
	since := time.Now()
	term.Type("/rename " + title + "\r")
	term.WaitFor(title+" · ", 20*time.Second)
	nameSynced(c, since, title)
	if name := lsName(c, s); name != s.Name {
		c.Failf("pfm ls names chat %s %q after the thread title %q arrived, want the --name %q",
			s.Chat.Session, name, title, s.Name)
	}
	fleetConsistent(c, s)
	return s
}

// chatNewInPane: the owner leaves the branch pane's picker for its shell and types `pfm chat new --engine
// {engine} --name {name}` in his project; pfm prints the chat's line (engine, name, backend, socket, pane),
// Herdr opens a tab named for it, and one live chat of kind and that name sits on the printed pane. The
// tab opens beside the owner's, so he moves to it with Herdr's next-tab key (Ctrl-b n).
func chatNewInPane(c *harness.Ctx, s State, engine, kind string) State {
	term := c.Terminal(s.Term)
	term.Press(harness.Esc)
	term.WaitFor(prompt)
	s.Name, s.Project = c.Unique(engine), s.Home+"/work/demo"
	sock := herdrSocket(s)
	term.Type("cd " + s.Project + " && pfm chat new --engine " + engine + " --name " + s.Name + "\r")
	var pane string
	term.WaitScreen("pfm chat new's line for "+s.Name, 20*time.Second, func(screen string) bool {
		for _, line := range strings.Split(screen, "\n") {
			f := strings.Fields(paneText(line))
			if len(f) == 6 && f[0] == engine && f[1] == s.Name && f[2] == "herdr" && f[3] == sock {
				pane = f[4]
				return true
			}
		}
		return false
	})
	awaitTopTab(term, "idle", s.Name)
	c.Poll("one live "+kind+" chat named "+s.Name+" with a live seat on "+pane, 20*time.Second,
		func() (bool, string) {
			rows := namedChats(c, sock)
			seats := liveSeats(c, sock)
			seen := fmt.Sprintf("chats %v, seats %v", rows, seats)
			if len(rows) != 1 || len(seats) != 1 {
				return false, seen
			}
			running := map[string]bool{}
			for _, session := range engines(processes(c)) {
				running[session] = true
			}
			for session, row := range rows {
				// A fresh engine's own session is the seat's once pfm has bound it: before that the row
				// names the id pfm registered the chat under, which no engine runs.
				if seat, ok := seats[session]; !ok || row != [2]string{kind, s.Name} || seat[1] != pane ||
					kind != "live-opencode" && !running[session] {
					return false, seen + fmt.Sprintf(", engine sessions %v", running)
				}
				s.Chat = Chat{Session: session, Seat: seats[session][0]}
			}
			return true, seen
		})
	s.Pane = pane
	if kind == "live-opencode" {
		s.Chat.Engine = freshOpenCode(c, s)
	} else {
		s.Chat.Engine = engineOf(c, s)
	}
	term.Press(harness.Ctrl('b'), harness.Key("n"))
	c.Poll("Herdr's focus on "+s.Name+"'s pane "+pane, stepBound, func() (bool, string) {
		got := focusedPane(c, s)
		return got == pane, "focused pane " + got
	})
	// Herdr's state moves focus before its client draws the tab, and keys typed in between are lost: the
	// owner types once his screen shows the engine's tab, where the shell line he typed is gone.
	term.WaitScreen(s.Name+"'s tab drawn in place of the shell", stepBound, func(screen string) bool {
		return !strings.Contains(screen, "--name "+s.Name)
	})
	return s
}

// freshOpenCode is the one OpenCode engine under the branch's Herdr server: a fresh OpenCode names its
// session nowhere a process shows, so the branch's only OpenCode is its chat's.
func freshOpenCode(c *harness.Ctx, s State) int {
	pid := 0
	c.Poll("one OpenCode engine under the Herdr server of "+herdrSocket(s), stepBound, func() (bool, string) {
		table := processes(c)
		server := herdrServer(table, s)
		var found []int
		for p, row := range table {
			if row.Engine == "opencode" && server != 0 && under(table, p, server) {
				found = append(found, p)
			}
		}
		if len(found) == 1 {
			pid = found[0]
		}
		return len(found) == 1, fmt.Sprintf("server %d, OpenCode engines %v", server, found)
	})
	return pid
}

// nameSynced waits for a pfm name-sync run that started after since to finish: the thread title a
// /rename writes (Codex's session_index row) fires pfm's name sync (pfm-name-sync.path, launchd's
// WatchPaths), so the name is judged once such a run is over.
func nameSynced(c *harness.Ctx, since time.Time, title string) {
	c.Poll("a pfm name-sync run finished after the thread title "+title, 20*time.Second, func() (bool, string) {
		for _, record := range activity(c, since, time.Time{}) {
			if strings.Contains(record, `"msg":"cmd.exit","cmd":"name-sync"`) {
				return true, record
			}
		}
		return false, "no name-sync exit in pfm's activity log since " + since.Format(time.RFC3339Nano)
	})
}
