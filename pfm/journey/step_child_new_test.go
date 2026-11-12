//go:build journey

package journey

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// childNew: in the chat's own tab the owner asks it to spawn a child through pfm's MCP `new` tool,
// not waiting for the child's answer, with a first prompt the child works on until a file the owner
// holds is gone. The chat's reply paints within 10 s while the child still works (escape chat-mesh/
// mcp-new-returns-before-child-ready); the receipt names the child on the chat's own Herdr socket;
// Herdr opens the child's tab; the child is one live Claude chat with its own seat and pane in the
// chat's workspace, its engine under the session's Herdr server, carrying its own pane variable, the
// chat's TERM, and no tmux pane, colour-off or project-directory variable inherited from the chat that
// spawned it (escape chat-mesh/spawn-child-from-mcp, the Claude-parent form). While it works the chat
// calls it, and the call queues behind its turn; the owner then lets the child finish. Meanwhile, in
// his second plain terminal, he starts the chat call-across calls (peerNew).
func childNew(c *harness.Ctx, s State) State {
	term, owner := c.Terminal(s.Term), c.Terminal(meshOwner)
	sock := herdrSocket(s)
	peerNew(c, &s)
	s.Mesh.ChildName = c.Unique("child")
	hold := s.Home + "/" + c.Unique("hold")
	if code := shellRun(owner, "hold", "touch "+hold, stepBound); code != 0 {
		c.Failf("touch %s exited %d:\n%s", hold, code, owner.Screen())
	}
	c.Cleanup(func() {
		if r := c.Read("rm", "-f", hold); r.Code != 0 {
			c.Logf("teardown: rm %s exited %d: %s", hold, r.Code, r.Stderr)
		}
	})
	term.Press(harness.Ctrl('b'), harness.Key("n"))
	term.WaitFor("❯", 20*time.Second)
	reply := c.Unique("spawned")
	childPrompt := `mock-engine: {\"type\":\"hold\",\"until_gone\":\"` + hold + `\"}`
	term.Type(`spawn a child mock-engine: [{"type":"mcp","tool":"new","input":{"name":"` + s.Mesh.ChildName +
		`","prompt":"` + childPrompt + `"}},{"type":"turn","reply":"` + reply + `"}]` + "\r")
	start := time.Now()
	term.WaitFor("⏺ "+reply, 10*time.Second)
	s.Mesh.Mark = reply
	c.Logf("the chat's reply painted %s after the ask", time.Since(start).Round(time.Millisecond))
	if r := c.Read("test", "-e", hold); r.Code != 0 {
		c.Failf("the hold file %s is gone before the step released it", hold)
	}
	receipt := mcpReceipt(c, s.Name, s.Chat.Session)
	awaitTopTab(term, "working", s.Mesh.ChildName)
	child := s
	c.Poll("one live Claude chat named "+s.Mesh.ChildName+" with its own live seat on "+sock, 20*time.Second,
		func() (bool, string) {
			rows, seats := namedChats(c, sock), liveSeats(c, sock)
			seen := fmt.Sprintf("chats %v, seats %v", rows, seats)
			var found []string
			for session, row := range rows {
				if row == [2]string{"live-claude", s.Mesh.ChildName} {
					found = append(found, session)
				}
			}
			if len(found) != 1 {
				return false, seen
			}
			seat, ok := seats[found[0]]
			if !ok || seat[1] == seats[s.Chat.Session][1] {
				return false, seen
			}
			child.Chat = Chat{Session: found[0], Seat: seat[0]}
			s.Mesh.ChildPane = seat[1]
			return true, seen
		})
	want := fmt.Sprintf("cc\t%s\therdr\t%s\t%s\tstarting", s.Mesh.ChildName, sock, s.Mesh.ChildPane)
	if receipt.Status != "ok" || receipt.Code != 0 || receipt.Message != want {
		c.Failf("MCP new's receipt = %+v, want status ok, code 0, message %q", receipt, want)
	}
	child.Chat.Engine = engineOf(c, child)
	s.Mesh.Child = child.Chat
	proc := procTable(c)
	parentEnv, perr := proc.Environ(s.Chat.Engine)
	childEnv, cerr := proc.Environ(child.Chat.Engine)
	if perr != nil || cerr != nil {
		c.Failf(
			"read the engines' environments: chat %d: %v; child %d: %v",
			s.Chat.Engine,
			perr,
			child.Chat.Engine,
			cerr,
		)
	}
	_, tmuxPane := childEnv["TMUX_PANE"]
	_, noColor := childEnv["NO_COLOR"]
	projectDir, inherited := childEnv["CLAUDE_PROJECT_DIR"]
	if childEnv["HERDR_PANE_ID"] != s.Mesh.ChildPane ||
		childEnv["HERDR_WORKSPACE_ID"] != parentEnv["HERDR_WORKSPACE_ID"] ||
		childEnv["HERDR_SOCKET_PATH"] != sock ||
		tmuxPane ||
		noColor ||
		inherited ||
		childEnv["TERM"] == "" || childEnv["TERM"] != parentEnv["TERM"] {
		c.Failf(
			"child engine %d environment: HERDR_PANE_ID=%q HERDR_WORKSPACE_ID=%q HERDR_SOCKET_PATH=%q TMUX_PANE set %v "+
				"NO_COLOR set %v CLAUDE_PROJECT_DIR set %v (%q) TERM=%q; want its own pane %s in the chat's workspace "+
				"%q on %s, neither TMUX_PANE, NO_COLOR nor CLAUDE_PROJECT_DIR, and the chat's TERM %q",
			child.Chat.Engine,
			childEnv["HERDR_PANE_ID"],
			childEnv["HERDR_WORKSPACE_ID"],
			childEnv["HERDR_SOCKET_PATH"],
			tmuxPane,
			noColor,
			inherited,
			projectDir,
			childEnv["TERM"],
			s.Mesh.ChildPane,
			parentEnv["HERDR_WORKSPACE_ID"],
			sock,
			parentEnv["TERM"],
		)
	}
	queued := c.Unique("queued")
	busyDoors(c, term, &s, hold, queued)
	if r := c.Read("rm", hold); r.Code != 0 {
		c.Failf("rm %s exited %d: %s", hold, r.Code, r.Stderr)
	}
	var state struct {
		State string `json:"state"`
	}
	c.Poll(s.Mesh.ChildName+" idle once its first turn and the call queued behind it answered", stepBound,
		func() (bool, string) {
			out := read(c, "pfm", "chat", "status", s.Mesh.ChildName, "--json")
			if json.Unmarshal([]byte(out), &state) != nil {
				return false, out
			}
			last := c.Read("pfm", "chat", "last", s.Mesh.ChildName)
			return state.State == "idle" && last.Stdout == queued+"\n", out + last.Stdout + last.Stderr
		})
	idleDoors(c, term, &s, hold, queued)
	fleetConsistent(c, s)
	return s
}
