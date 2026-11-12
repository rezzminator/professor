//go:build journey

package journey

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// childNew: in the chat's own tab the owner asks it to spawn a child through pfm's MCP `new` tool,
// not waiting for the child's answer, with a first prompt the child works on until a file the owner
// holds is gone. The chat's reply paints within 10 s while the child still works (escape chat-mesh/
// mcp-new-returns-before-child-ready); the receipt names the child on the chat's own Herdr socket;
// Herdr opens the child's tab; the child is one live Claude chat with its own seat and pane in the
// chat's workspace, its engine under the session's Herdr server, carrying its own pane variable and
// no tmux pane or colour-off variable inherited from the chat that spawned it (escape chat-mesh/
// spawn-child-from-mcp, the Claude-parent form). The owner then lets the child finish.
func childNew(c *harness.Ctx, s State) State {
	term, owner := c.Terminal(s.Term), c.Terminal(meshOwner)
	sock := herdrSocket(s)
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
	c.Logf("the chat's reply painted %s after the ask", time.Since(start).Round(time.Millisecond))
	if r := c.Read("test", "-e", hold); r.Code != 0 {
		c.Failf("the hold file %s is gone before the step released it", hold)
	}
	receipt := mcpReceipt(c, s.Name, s.Chat.Session)
	term.WaitScreen("a Herdr tab named "+s.Mesh.ChildName, stepBound, func(screen string) bool {
		top, _, _ := strings.Cut(screen, "\n")
		return slices.Contains(strings.Fields(top), s.Mesh.ChildName)
	})
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
	status := read(c, "pfm", "chat", "status", s.Mesh.ChildName, "--json")
	var state struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal([]byte(status), &state); err != nil || state.State != "working" {
		c.Failf("pfm chat status %s while its first turn holds = %q (%v), want state working:\n%s",
			s.Mesh.ChildName, state.State, err, status)
	}
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
	if childEnv["HERDR_PANE_ID"] != s.Mesh.ChildPane ||
		childEnv["HERDR_WORKSPACE_ID"] != parentEnv["HERDR_WORKSPACE_ID"] ||
		childEnv["HERDR_SOCKET_PATH"] != sock ||
		tmuxPane ||
		noColor {
		c.Failf(
			"child engine %d environment: HERDR_PANE_ID=%q HERDR_WORKSPACE_ID=%q HERDR_SOCKET_PATH=%q TMUX_PANE set %v "+
				"NO_COLOR set %v; want its own pane %s in the chat's workspace %q on %s, neither TMUX_PANE nor NO_COLOR",
			child.Chat.Engine,
			childEnv["HERDR_PANE_ID"],
			childEnv["HERDR_WORKSPACE_ID"],
			childEnv["HERDR_SOCKET_PATH"],
			tmuxPane,
			noColor,
			s.Mesh.ChildPane,
			parentEnv["HERDR_WORKSPACE_ID"],
			sock,
		)
	}
	if r := c.Read("rm", hold); r.Code != 0 {
		c.Failf("rm %s exited %d: %s", hold, r.Code, r.Stderr)
	}
	c.Poll(s.Mesh.ChildName+" idle once its first turn is released", stepBound, func() (bool, string) {
		out := read(c, "pfm", "chat", "status", s.Mesh.ChildName, "--json")
		if json.Unmarshal([]byte(out), &state) != nil {
			return false, out
		}
		return state.State == "idle", out
	})
	fleetConsistent(c, s)
	return s
}

// mcpReceipt is the receipt of the latest MCP tool call of the chat name, read from its Claude
// transcript, session's file.
func mcpReceipt(c *harness.Ctx, name, session string) (receipt struct {
	Status  string `json:"status"`
	Code    int    `json:"code"`
	Message string `json:"message"`
},
) {
	transcript := read(c, "sh", "-c", "cat ~/.claude/projects/*/"+session+".jsonl")
	var last string
	for _, line := range strings.Split(transcript, "\n") {
		var record struct {
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		var blocks []struct {
			Type    string `json:"type"`
			Content string `json:"content"`
		}
		if json.Unmarshal([]byte(line), &record) != nil || json.Unmarshal(record.Message.Content, &blocks) != nil {
			continue
		}
		for _, block := range blocks {
			if block.Type == "tool_result" {
				last = block.Content
			}
		}
	}
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal([]byte(last), &result); err != nil || len(result.Content) != 1 ||
		json.Unmarshal([]byte(result.Content[0].Text), &receipt) != nil {
		c.Failf("no MCP tool receipt readable in %s's transcript %s (last tool result %q)", name, session, last)
	}
	return receipt
}
