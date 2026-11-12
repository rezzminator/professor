//go:build journey

package journey

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// meshPeer is herdr-mesh's second plain terminal, where the owner starts a chat outside Herdr.
const meshPeer = "mesh-peer"

// callAcross: in a second plain terminal the owner starts a Claude chat with `pfm chat new --engine cc`, outside Herdr,
// and attaches the tmux session it prints. From the branch's Herdr chat he has the chat call it
// through pfm's MCP with a message that makes it, once a file he holds is gone, call back and answer.
// pfm types the call into the peer's own tmux pane on its own socket (escape chat-mesh/call-from-cli,
// to-tmux, delivered once to its actual pane), signed with the caller's session and its reply address
// (escape chat-mesh/call-from-mcp). He removes the file: the peer calls the Herdr chat back into its
// own Herdr pane, both replies paint, and each transcript grows by exactly those records.
func callAcross(c *harness.Ctx, s State) State {
	term, owner, peerTerm := c.Terminal(s.Term), c.Terminal(meshOwner), c.Terminal(meshPeer)
	peerTerm.WaitFor(prompt)
	s.Mesh.PeerName = c.Unique("peer")
	peerTerm.Type("cd $HOME/work/demo && pfm chat new --engine cc --name " + s.Mesh.PeerName + "\r")
	var attach string
	peerTerm.WaitScreen("pfm chat new's lines for "+s.Mesh.PeerName, 30*time.Second, func(screen string) bool {
		lines := strings.Split(screen, "\n")
		for i, line := range lines {
			if f := strings.Fields(line); len(f) == 5 && f[0] == "cc" && f[1] == s.Mesh.PeerName && i+1 < len(lines) {
				s.Mesh.PeerSock = f[2]
				attach = strings.TrimSpace(lines[i+1])
				return f[3] == "named" && f[4] == "listed"
			}
		}
		return false
	})
	c.Cleanup(func() {
		if r := c.Read("pfm", "chat", "end", s.Mesh.PeerName); r.Code != 0 {
			c.Logf("teardown: pfm chat end %s exited %d: %s%s", s.Mesh.PeerName, r.Code, r.Stderr, r.Stdout)
		}
	})
	if want := "attach: tmux -L " + s.Mesh.PeerSock + " attach -t " + s.Mesh.PeerSock; attach != want {
		c.Failf("pfm chat new %s printed %q under its row, want %q", s.Mesh.PeerName, attach, want)
	}
	peerTerm.Type(strings.TrimPrefix(attach, "attach: ") + "\r")
	peerTerm.WaitFor("❯", 30*time.Second)
	c.Poll("one live Claude chat named "+s.Mesh.PeerName+" on "+s.Mesh.PeerSock, stepBound, func() (bool, string) {
		rows := namedChats(c, s.Mesh.PeerSock)
		for session, row := range rows {
			s.Mesh.Peer = Chat{Session: session, Name: s.Mesh.PeerName}
			return len(rows) == 1 && row == [2]string{"live-claude", s.Mesh.PeerName}, fmt.Sprint(rows)
		}
		return false, "no row"
	})
	panes := strings.Fields(read(c, "tmux", "-L", s.Mesh.PeerSock, "list-panes", "-a", "-F", "#{pane_id}"))
	if len(panes) != 1 {
		c.Failf("tmux -L %s holds panes %q, want the peer's one", s.Mesh.PeerSock, panes)
	}
	parentPane := liveSeats(c, herdrSocket(s))[s.Chat.Session][1]

	hold := s.Home + "/" + c.Unique("hold")
	if code := shellRun(owner, "hold-peer", "touch "+hold, stepBound); code != 0 {
		c.Failf("touch %s exited %d:\n%s", hold, code, owner.Screen())
	}
	c.Cleanup(func() {
		if r := c.Read("rm", "-f", hold); r.Code != 0 {
			c.Logf("teardown: rm %s exited %d: %s", hold, r.Code, r.Stderr)
		}
	})
	back, across, called := c.Unique("back"), c.Unique("across"), c.Unique("called")
	backAsk := directive(turnStep(back))
	peerAsk := directive(map[string]any{"type": "hold", "until_gone": hold},
		mcpStep("call", map[string]any{"target": s.Name, "message": backAsk}), turnStep(across))
	callAsk := "call across " + directive(
		mcpStep("call", map[string]any{"target": s.Mesh.PeerName, "message": peerAsk}),
		turnStep(called),
	)
	term.Press(harness.Ctrl('b'), harness.Key("n"))
	term.WaitScreen("the session's first tab", 20*time.Second, func(screen string) bool {
		return composerLine(screen) == "" && strings.Contains(screen, "pfm chat new --engine cc --name "+s.Name)
	})
	term.Press(harness.Ctrl('b'), harness.Key("n"))
	term.WaitScreen("the chat's tab", 20*time.Second, func(screen string) bool {
		return strings.Contains(screen, "spawn a child")
	})
	parentBefore := entries(c, s.Name)
	term.Type(callAsk + "\r")
	term.WaitFor("⏺ "+called, 30*time.Second)
	receipt := mcpReceipt(c, s.Name, s.Chat.Session)
	if want := fmt.Sprintf(
		"called LIVE into %q — typed inline and submitted (Enter confirmed, input cleared)",
		panes[0],
	); receipt.Status != "delivered" || receipt.Code != 0 ||
		receipt.Message != want {
		c.Failf("%s's MCP call into %s = %+v, want status delivered, code 0, message %q",
			s.Name, s.Mesh.PeerName, receipt, want)
	}
	signedByParent := peerAsk + "  — sid " + s.Chat.Session[:8] + " · to reply: call " + s.Name + " <message>"
	c.Poll(s.Mesh.PeerName+"'s transcript holding the one signed call", stepBound, func() (bool, string) {
		got := entries(c, s.Mesh.PeerName)
		return slices.Equal(got, []entry{{"user", signedByParent}}), fmt.Sprintf("%+v", got)
	})

	// The peer's call back must meet an idle agent: Herdr marks the caller's turn over once its hook
	// lands, later than the paint on a loaded machine, and a call meeting a working agent takes Herdr's
	// raw path, whose receipt is worded differently.
	c.Poll("Herdr status idle for "+parentPane, 20*time.Second, func() (bool, string) {
		got := herdrStatus(c, s, parentPane)
		return got == "idle", "status " + got
	})
	if r := c.Read("rm", hold); r.Code != 0 {
		c.Failf("rm %s exited %d: %s", hold, r.Code, r.Stderr)
	}
	term.WaitFor("⏺ "+back, 30*time.Second)
	peerTerm.WaitFor("⏺ "+across, 30*time.Second)
	peerWant := []entry{{"user", signedByParent}, {"tool", ""}, {"assistant", across}}
	parentWant := append(
		slices.Clone(parentBefore),
		entry{"user", callAsk},
		entry{"tool", ""},
		entry{"assistant", called},
		entry{
			"user",
			backAsk + "  — sid " + s.Mesh.Peer.Session[:8] + " · to reply: call " + s.Mesh.PeerName + " <message>",
		},
		entry{"assistant", back},
	)
	c.Poll("both transcripts settled", stepBound, func() (bool, string) {
		peer, parent := entries(c, s.Mesh.PeerName), entries(c, s.Name)
		return slices.Equal(peer, peerWant) && slices.Equal(parent, parentWant),
			fmt.Sprintf(
				"%s: %+v\nwant %+v\n%s: %+v\nwant %+v",
				s.Mesh.PeerName,
				peer,
				peerWant,
				s.Name,
				parent,
				parentWant,
			)
	})
	delivered := herdrDelivered(parentPane)
	receipt = mcpReceipt(c, s.Mesh.PeerName, s.Mesh.Peer.Session)
	status := "delivered_unproven" // the receipt says the delivery stands though its turn went unproven
	if strings.HasSuffix(receipt.Message, " — Herdr turn confirmed") {
		status = "delivered"
	}
	if receipt.Status != status || receipt.Code != 0 || !delivered.MatchString(receipt.Message) {
		c.Failf("%s's MCP call back into %s = %+v, want status %s, code 0, message matching %s",
			s.Mesh.PeerName, s.Name, receipt, status, delivered)
	}
	return s
}
