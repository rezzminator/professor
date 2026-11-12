//go:build journey

package journey

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// plainCodex: in a plain terminal the owner goes to the demo project and types cx, pfm's command for
// a Codex chat in a tmux server of its own; the fake Codex's composer comes up, and pfm lists it as the
// one live Codex chat on that socket, run by one engine. When the branch's subtree ends, failed or
// not, the chat is ended and no engine of it is left.
func plainCodex(c *harness.Ctx, s State) State {
	s.Term, s.Project = "plain-codex", filepath.Join(s.Home, "work", "demo")
	term := c.Terminal(s.Term)
	term.WaitFor(prompt)
	c.Cleanup(func() { stopPlainChat(c, &s, term) })
	term.Type("cd $HOME/work/demo && cx\r")
	term.WaitFor("Ask Codex to do anything", 20*time.Second)
	socket := attached(c, term)
	c.Poll("one live Codex chat on tmux socket "+socket+" with one engine", stepBound, func() (bool, string) {
		rows, listed := liveChats(c, socket)
		var pids []int
		for _, id := range listed {
			for pid, session := range engines(processes(c)) {
				if session == id {
					pids = append(pids, pid)
				}
			}
		}
		seen := fmt.Sprintf("chats %v (listed %v), engines %v", rows, listed, pids)
		if len(listed) != 1 || rows[listed[0]][0] != "live-codex" || len(pids) != 1 {
			return false, seen
		}
		s.Chat = Chat{Session: listed[0], Engine: pids[0]}
		return true, seen
	})
	return s
}

// plainCodexEnd: the owner types /exit in the Codex chat; its composer leaves the screen and the
// terminal, which the chat owned, closes. No live row, engine or tmux process of the chat is left.
func plainCodexEnd(c *harness.Ctx, s State) State {
	term := c.Terminal(s.Term)
	socket := attached(c, term)
	term.Type("/exit\r")
	term.WaitGone("Ask Codex to do anything", 20*time.Second)
	plainGone(c, s, term, socket)
	return s
}
