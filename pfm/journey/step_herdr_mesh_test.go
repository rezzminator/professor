//go:build journey

package journey

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// meshOwner is herdr-mesh's plain terminal, where the owner runs pfm's commands beside his Herdr session.
const meshOwner = "mesh-owner"

// Mesh is what herdr-mesh's steps hand on beside the branch's own chat: the chat it spawned through
// pfm's MCP, and the tmux-hosted chat it calls across multiplexers.
type Mesh struct {
	Child     Chat   // the chat child-new spawned
	ChildName string // its pfm name
	ChildPane string // its Herdr pane
	Peer      Chat   // the tmux-hosted chat call-across started
	PeerName  string // its pfm name
	PeerSock  string // its tmux socket
	Mark      string // the reply last painted in the chat's own tab
}

// herdrMesh: in a plain terminal the owner turns pfm's chat MCP server on (`pfm mcp chat enable`, off
// by default), so the chats he starts next can reach each other; pfm's list of servers then reads it
// on from his config. Then he starts a Herdr session of the branch's own. When the branch ends the
// server is turned off again, so no later step meets a config the day changed. The branch's forks
// run side by side, each in a Herdr session, plain terminals and chats of its own.
func herdrMesh(c *harness.Ctx, s State) State {
	owner := c.Terminal("mesh-enable")
	owner.WaitFor(prompt)
	if code := shellRun(owner, "mcp-enable", "pfm mcp chat enable", stepBound); code != 0 {
		c.Failf("pfm mcp chat enable exited %d:\n%s", code, owner.Screen())
	}
	c.Cleanup(func() {
		if r := c.Read("pfm", "mcp", "chat", "disable"); r.Code != 0 {
			c.Logf("teardown: pfm mcp chat disable exited %d: %s%s", r.Code, r.Stderr, r.Stdout)
		}
	})
	if got, want := strings.TrimSpace(read(c, "pfm", "mcp", "ls")), "chat\ttrue\tfile"; got != want {
		c.Failf("pfm mcp ls after the enable = %q, want %q", got, want)
	}
	return herdrSession(c, s)
}

// meshAlone: in a Herdr session of the fork's own the owner starts a Claude chat in a project of its
// own; alone there and mid-turn it plays every function on itself and on a name no chat holds
// (aloneDoors). Then the owner starts a chat and ends it before its first prompt, and the chat plays
// it at both doors (deadChat).
func meshAlone(c *harness.Ctx, s State) State {
	s = herdrSession(c, s)
	s = meshCaller(c, s, "alone", c.Unique("lone"))
	term := chatTab(c, s)
	aloneDoors(c, term, &s)
	deadChat(c, &s)
	return s
}

// meshHost: in a Herdr session of the fork's own the owner starts a Claude chat, and from his plain
// terminal a tmux-hosted chat by name (`--backend tmux`), unattached; then the chat plays where pfm
// places what it starts (hostRule), and the owner calls the tmux-hosted chat (peerRows).
func meshHost(c *harness.Ctx, s State) State {
	s = herdrSession(c, s)
	s = meshCaller(c, s, "host", "demo")
	owner := c.Terminal(meshOwner)
	owner.WaitFor(prompt)
	s.Mesh.PeerName = c.Unique("peer")
	out, errs, code := typedRun(c, owner, "peer-new",
		"cd $HOME/work/demo && pfm chat new --engine cc --backend tmux --name "+s.Mesh.PeerName)
	c.Cleanup(func() {
		if r := c.Read("pfm", "chat", "status", s.Mesh.PeerName, "--json"); r.Code != 0 {
			return
		}
		if r := c.Read("pfm", "chat", "end", s.Mesh.PeerName); r.Code != 0 {
			c.Logf("teardown: pfm chat end %s exited %d: %s%s", s.Mesh.PeerName, r.Code, r.Stderr, r.Stdout)
		}
	})
	f := strings.Fields(out)
	if code != 0 || errs != "" || len(f) < 3 ||
		out != fmt.Sprintf("cc\t%s\t%s\tnamed\tlisted\nattach: tmux -L %[2]s attach -t %[2]s\n",
			s.Mesh.PeerName, f[2]) {
		c.Failf("pfm chat new --backend tmux --name %s: exit %d, stdout %q, stderr %q; want exit 0 and its row and "+
			"attach line", s.Mesh.PeerName, code, out, errs)
	}
	s.Mesh.PeerSock = f[2]
	c.Poll("one live Claude chat named "+s.Mesh.PeerName+" on "+s.Mesh.PeerSock, stepBound, func() (bool, string) {
		rows := namedChats(c, s.Mesh.PeerSock)
		for session, row := range rows {
			s.Mesh.Peer = Chat{Session: session, Name: s.Mesh.PeerName}
			return len(rows) == 1 && row == [2]string{"live-claude", s.Mesh.PeerName}, fmt.Sprint(rows)
		}
		return false, "no row"
	})
	chatTab(c, s)
	hostRule(c, &s)
	peerRows(c, &s)
	return s
}

// meshStarted: in a Herdr session of the fork's own the owner starts a Claude chat, which starts a
// Claude and a Codex chat and plays every function on the Codex one (startedDoors).
func meshStarted(c *harness.Ctx, s State) State {
	s = herdrSession(c, s)
	s = meshCaller(c, s, "starter", "demo")
	startedDoors(c, chatTab(c, s), &s)
	return s
}

// chatTab moves s's Herdr session to its next tab, the chat meshCaller opened, and waits for its composer.
func chatTab(c *harness.Ctx, s State) *harness.Terminal {
	term := c.Terminal(s.Term)
	term.Press(harness.Ctrl('b'), harness.Key("n"))
	term.WaitFor("❯", 20*time.Second)
	return term
}

// statsRows: in a plain terminal the owner opens pfm's picker on its Stats tab: each live chat of
// names is a row of its own whose memory is read from its own processes, never one row for every
// Herdr seat of a server with no process (escape picker/stats-per-seat).
func statsRows(c *harness.Ctx, names ...string) {
	owner := c.Terminal(meshOwner)
	owner.WaitFor(prompt)
	owner.Type("pfm\r")
	owner.WaitFor("New chat", 20*time.Second)
	owner.Press(harness.Tab)
	owner.WaitScreen("a Stats row of its own with a memory reading for each of "+strings.Join(names, ", "), stepBound,
		func(screen string) bool {
			for _, name := range names {
				row := regexp.MustCompile(
					`[\s│›]` + regexp.QuoteMeta(name) + `\s+(claude|codex)\s+\S+\s+\d+(\.\d)?[KMGT]\s`,
				)
				if len(row.FindAllString(screen, -1)) != 1 {
					return false
				}
			}
			return true
		})
	owner.Press(harness.Esc)
	promptBack(owner, "the prompt back under the closed picker")
}

// meshUnnamed: in a Herdr session of the fork's own the owner types `claude` in his project's shell:
// the chat runs unnamed, its seat's handle `seat:{id}`. From inside it asks who it is at both doors
// and reads its own status by that handle (escape chat-mesh/unnamed-seat-whoami). The owner moves its
// pane to a workspace of its own (`herdr pane move --new-workspace`) and calls it by its handle with
// the same questions: the moved chat still finds its pane (escape chat-mesh/moved-workspace); then it
// exits itself through the MCP door and leaves no live seat behind.
func meshUnnamed(c *harness.Ctx, s State) State {
	s = herdrSession(c, s)
	term, sock := c.Terminal(s.Term), herdrSocket(s)
	term.Press(harness.Esc)
	term.WaitFor(prompt, 20*time.Second)
	s.Project = s.Home + "/work/demo"
	term.Type("cd " + s.Project + " && claude\r")
	term.WaitFor("❯", 20*time.Second)
	s.Chat = oneLiveChat(c, s, "live-claude")
	s.Chat.Engine = engineOf(c, s)
	handle := "seat:" + s.Chat.Seat
	s.Name = handle
	c.Cleanup(func() {
		if _, live := liveSeats(c, sock)[s.Chat.Session]; !live {
			return
		}
		if r := c.Read("pfm", "chat", "end", handle); r.Code != 0 {
			c.Logf("teardown: pfm chat end %s exited %d: %s%s", handle, r.Code, r.Stderr, r.Stdout)
		}
	})
	whoami := func(pane string) []door {
		return []door{
			cliIs("pfm whoami", 0, handle+"\n"),
			cliIs("pfm chat whoami", 0, handle+"\n"),
			mcpJSON("whoami", map[string]any{}, map[string]any{
				"engine":      "cc",
				"id":          s.Chat.Session,
				"pane":        pane,
				"session":     handle,
				"socket_name": "herdr.sock",
				"socket_path": sock,
				"source":      "mcp-proxy",
				"status":      "ok",
			}),
			// The handle whoami prints resolves to the chat, which holds no name.
			cliIs(`pfm chat resolve "$(pfm chat whoami)"`, 0, sock+"\t\t"+s.Chat.Session+"\n"),
			// Status finds the chat by that handle; its name, state and last line are its turn's.
			{step: cli(`pfm chat status "$(pfm chat whoami)" --json`), check: func(got answer) string {
				var status map[string]any
				if err := json.Unmarshal([]byte(got.Text), &status); err != nil || got.Code != 0 ||
					status["session_id"] != s.Chat.Session || status["engine"] != "cc" || status["cwd"] != s.Project ||
					status["socket"] != sock {
					return fmt.Sprintf("want code 0, the status of session %s (engine cc, cwd %s, socket %s) (%v)",
						s.Chat.Session, s.Project, sock, err)
				}
				return ""
			}},
		}
	}
	playDoors(c, term, &s, "the unnamed chat", whoami(s.Pane)...)
	if r := c.Read(herdrCLI(s, "pane", "move", s.Pane, "--new-workspace")...); r.Code != 0 {
		c.Failf("herdr pane move --new-workspace %s exited %d: %s%s", s.Pane, r.Code, r.Stderr, r.Stdout)
	}
	moved := s.Pane
	c.Poll(handle+"'s seat on a pane of another workspace", stepBound, func() (bool, string) {
		seat := liveSeats(c, sock)[s.Chat.Session]
		moved = seat[1]
		return strings.Split(moved, ":")[0] != strings.Split(s.Pane, ":")[0], fmt.Sprintf("seat %v", seat)
	})
	owner := c.Terminal(meshOwner)
	owner.WaitFor(prompt)
	cells := append(whoami(moved), door{
		step: mcpStep("exit", map[string]any{}),
		check: receiptMatch("ok", 0, `exit scheduled: killed `+s.Chat.Session+`\tclosing pane `+regexp.QuoteMeta(moved)+
			` on socket \S+ — reply one line and end this turn; the pane closes once the turn is over`),
	})
	steps := make([]map[string]any, len(cells))
	for index, cell := range cells {
		steps[index] = cell.step
	}
	answers := played(c, &s, steps, func(turn, reply string) {
		if _, errs, code := typedRun(c, owner, "call-moved",
			"pfm chat call --allow-unsigned "+handle+" "+shellQuote(turn)); code != 0 {
			c.Failf("pfm chat call %s after its move exited %d: %s", handle, code, errs)
		}
		c.Poll(handle+"'s reply "+reply, stepBound, func() (bool, string) {
			r := c.Read("pfm", "chat", "last", s.Chat.Session)
			return r.Stdout == reply+"\n", r.Stdout + r.Stderr
		})
	})
	for index, cell := range cells {
		problem := ""
		if cell.check != nil {
			problem = cell.check(answers[index])
		} else if answers[index] != cell.want {
			problem = fmt.Sprintf("want code %d, text %q", cell.want.Code, cell.want.Text)
		}
		if problem != "" {
			c.Failf("the moved chat's cell %v: got code %d, text %q; %s", cell.step, answers[index].Code,
				answers[index].Text, problem)
		}
	}
	c.Poll(handle+" exited, no live seat left on "+sock, stepBound, func() (bool, string) {
		seats := liveSeats(c, sock)
		return len(seats) == 0, fmt.Sprintf("seats %v", seats)
	})
	return s
}
