//go:build journey

package journey

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// meshPeer is herdr-mesh's second plain terminal, where the owner starts a chat outside Herdr.
const meshPeer = "mesh-peer"

// peerNew: in a second plain terminal the owner starts a Claude chat with `pfm chat new --engine cc`,
// outside Herdr; callAcross reads what it printed.
func peerNew(c *harness.Ctx, s *State) {
	peerTerm := c.Terminal(meshPeer)
	peerTerm.WaitFor(prompt)
	s.Mesh.PeerName = c.Unique("peer")
	peerTerm.Type("cd $HOME/work/demo && pfm chat new --engine cc --name " + s.Mesh.PeerName + "\r")
	c.Cleanup(func() {
		if r := c.Read("pfm", "chat", "end", s.Mesh.PeerName); r.Code != 0 {
			c.Logf("teardown: pfm chat end %s exited %d: %s%s", s.Mesh.PeerName, r.Code, r.Stderr, r.Stdout)
		}
	})
}

// callAcross: the owner reads the lines `pfm chat new` printed in his second plain terminal (peerNew)
// and attaches the tmux session they name. From the branch's Herdr chat he has the chat call it
// through pfm's MCP with a message that makes it, once a file he holds is gone, call back and answer.
// pfm types the call into the peer's own tmux pane on its own socket (escape chat-mesh/call-from-cli,
// to-tmux, delivered once to its actual pane), signed with the caller's session and its reply address
// (escape chat-mesh/call-from-mcp). He removes the file: the peer calls the Herdr chat back into its
// own Herdr pane, both replies paint, and each transcript grows by exactly those records.
func callAcross(c *harness.Ctx, s State) State {
	term, owner, peerTerm := c.Terminal(s.Term), c.Terminal(meshOwner), c.Terminal(meshPeer)
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
		return strings.Contains(screen, "⏺ "+s.Mesh.Mark)
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
	s.Mesh.Mark = back
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

// peerRows: from his plain terminal the owner leaves a draft in the composer of the tmux-hosted peer,
// idle and unattached, and calls it: the call is delivered, the draft is back in the composer
// afterwards, and the transcript grows by the call and its answer alone (escape
// chat-mesh/call-keeps-draft). He calls it with a message over 720 runes: pfm pastes it whole and the
// peer's transcript holds it as sent, never a path to a file it was spilled to (escape
// chat-mesh/call-long-inline). He calls the
// peer with a turn that holds until a file he holds is gone and calls again mid-turn: the second call
// queues behind the turn (escape chat-mesh/call-busy-queues), and once he removes the file the peer
// answers both, in order.
func peerRows(c *harness.Ctx, s *State) {
	owner, peer := c.Terminal(meshOwner), s.Mesh.PeerName
	panes := strings.Fields(read(c, "tmux", "-L", s.Mesh.PeerSock, "list-panes", "-a", "-F", "#{pane_id}"))
	if len(panes) != 1 {
		c.Failf("tmux -L %s holds panes %q, want the peer's one", s.Mesh.PeerSock, panes)
	}
	pane := panes[0]
	calls := func(label string, want *regexp.Regexp, steps ...map[string]any) entry {
		message := directive(steps...)
		out, errs, code := typedRun(c, owner, label, "pfm chat call --allow-unsigned "+peer+" "+shellQuote(message))
		if code != 0 || !want.MatchString(out) || errs != unsignedFromOwner {
			c.Failf("pfm chat call %s (%s): exit %d, stdout %q, stderr %q; want exit 0, stdout matching %s, stderr %q",
				peer, label, code, out, errs, want, unsignedFromOwner)
		}
		return entry{"user", message + unsignedFooter}
	}
	live := func(receipt string) *regexp.Regexp {
		return regexp.MustCompile("^" + regexp.QuoteMeta(fmt.Sprintf(receipt, pane)) + "\n" + proofOf(`(?s:.*)`) + "$")
	}
	inline := "called LIVE into %q — typed inline and submitted (Enter confirmed, input cleared)"
	settled := func(what string, want []entry) {
		c.Poll(peer+"'s transcript "+what, stepBound, func() (bool, string) {
			got := entries(c, peer)
			return slices.Equal(got, want), fmt.Sprintf("%+v\nwant %+v", got, want)
		})
	}
	composer := func(want string) {
		c.Poll(peer+"'s composer "+strconv.Quote(want), stepBound, func() (bool, string) {
			screen := read(c, "tmux", "-L", s.Mesh.PeerSock, "capture-pane", "-p", "-t", pane)
			return composerLine(screen) == want, screen
		})
	}
	want := entries(c, peer)

	// A key typed while the peer still ends its turn is the agent's to drop: the draft and every call
	// that is not meant to queue wait for it idle, as the owner waits for the answer to stop.
	idle := func() {
		c.Poll(peer+" idle", stepBound, func() (bool, string) {
			r := c.Read("pfm", "chat", "status", peer, "--json")
			var status struct {
				State string `json:"state"`
			}
			return json.Unmarshal([]byte(r.Stdout), &status) == nil && status.State == "idle", r.Stdout + r.Stderr
		})
	}
	idle()
	draft := c.Unique("draft")
	if r := c.Read("tmux", "-L", s.Mesh.PeerSock, "send-keys", "-t", pane, "-l", draft); r.Code != 0 {
		c.Failf("tmux send-keys %s exited %d: %s", draft, r.Code, r.Stderr)
	}
	composer("❯ " + draft)
	kept := c.Unique("kept")
	want = append(want, calls("draft-call", live(inline), turnStep(kept)), entry{"assistant", kept})
	settled("holding the call over the draft and no draft", want)
	composer("❯ " + draft)
	erase := append([]string{"tmux", "-L", s.Mesh.PeerSock, "send-keys", "-t", pane}, slices.Repeat([]string{"BSpace"},
		len(draft))...)
	if r := c.Read(erase...); r.Code != 0 {
		c.Failf("tmux send-keys BSpace exited %d: %s", r.Code, r.Stderr)
	}
	composer("❯")

	idle()
	want = panelCall(c, s, pane, want, calls, live(inline), settled)
	idle()
	long := c.Unique("long") + " " + strings.Repeat("ж", 740)
	want = append(want, calls("long-call",
		live("called LIVE PASTE into %q — bracketed-paste body submitted (Enter confirmed, input cleared)"),
		turnStep(long)), entry{"assistant", long})
	settled("holding the long call whole", want)

	hold := s.Home + "/" + c.Unique("hold")
	if r := c.Read("touch", hold); r.Code != 0 {
		c.Failf("touch %s exited %d: %s", hold, r.Code, r.Stderr)
	}
	c.Cleanup(func() {
		if r := c.Read("rm", "-f", hold); r.Code != 0 {
			c.Logf("teardown: rm %s exited %d: %s", hold, r.Code, r.Stderr)
		}
	})
	held, behind := c.Unique("held"), c.Unique("behind")
	idle()
	first := calls("hold-call", live(inline), map[string]any{"type": "hold", "until_gone": hold}, turnStep(held))
	settled("holding the held call", append(slices.Clone(want), first))
	queued := live(
		"queued into %q — busy Claude accepted the turn without interruption (Enter confirmed, input cleared)",
	)
	second := calls("queued-call", queued, turnStep(behind))
	if r := c.Read("rm", hold); r.Code != 0 {
		c.Failf("rm %s exited %d: %s", hold, r.Code, r.Stderr)
	}
	settled("answering the held call, then the one queued behind it",
		append(want, first, entry{"assistant", held}, second, entry{"assistant", behind}))
}

// panelCall: the owner calls the peer with a turn that starts a background agent, focuses its agents panel
// with the left arrow and calls again: pfm hands the keyboard back with one Escape and the call lands
// (escape chat-mesh/call-over-agents-panel); the agent's end reports in its own turn. It returns the
// peer's entries after.
func panelCall(
	c *harness.Ctx, s *State, pane string, want []entry, calls func(string, *regexp.Regexp, ...map[string]any) entry,
	inline *regexp.Regexp, settled func(string, []entry),
) []entry {
	peer := s.Mesh.PeerName
	gate, agent, started, over := s.Home+"/"+c.Unique("agent-gate"), c.Unique("agent"), c.Unique("started"),
		c.Unique("over")
	if r := c.Read("touch", gate); r.Code != 0 {
		c.Failf("touch %s exited %d: %s", gate, r.Code, r.Stderr)
	}
	c.Cleanup(func() {
		if r := c.Read("rm", "-f", gate); r.Code != 0 {
			c.Logf("teardown: rm %s exited %d: %s", gate, r.Code, r.Stderr)
		}
	})
	capture := func() string { return read(c, "tmux", "-L", s.Mesh.PeerSock, "capture-pane", "-p", "-t", pane) }
	want = append(want, calls("agent-call", inline,
		map[string]any{"type": "background_agent", "name": agent, "until_gone": gate}, turnStep(started)),
		entry{"assistant", started})
	settled("holding the call that started an agent", want)
	c.Poll(peer+" waiting on its agent", stepBound, func() (bool, string) {
		screen := capture()
		return strings.Contains(screen, "✻ Waiting for 1 background agent to finish"), screen
	})
	if r := c.Read("tmux", "-L", s.Mesh.PeerSock, "send-keys", "-t", pane, "Left"); r.Code != 0 {
		c.Failf("tmux send-keys Left exited %d: %s", r.Code, r.Stderr)
	}
	c.Poll(peer+"'s agents panel focused", stepBound, func() (bool, string) {
		screen := capture()
		return strings.Contains(screen, "↑/↓ to select · Enter to view") && strings.Contains(screen, "❯ ⏺ main"), screen
	})
	want = append(want, calls("panel-call", inline, turnStep(over)), entry{"assistant", over})
	settled("holding the call over the focused agents panel", want)
	if screen := capture(); strings.Contains(screen, "↑/↓ to select") {
		c.Failf("%s's agents panel still holds the keyboard after the call:\n%s", peer, screen)
	}
	if r := c.Read("rm", gate); r.Code != 0 {
		c.Failf("rm %s exited %d: %s", gate, r.Code, r.Stderr)
	}
	want = append(want, entry{"assistant", `Agent "` + agent + `" finished`})
	settled("holding the agent's result", want)
	return want
}

// proofOf is the pattern of the delivery proof pfm prints under a live call's receipt: the target
// pane's tail, its lines matching tail.
func proofOf(tail string) string {
	return "--- delivery proof: target pane tail ---\n" + tail + "--- end delivery proof ---\n"
}
