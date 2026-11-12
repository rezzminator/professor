//go:build journey

package journey

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// The cells of the chat-function matrix each herdr-mesh step plays from inside its live Claude chat,
// in the state the step holds: a chat alone (aloneDoors), the child working and then idle
// (busyDoors, idleDoors), the child ended and reopened (deadChildDoors, reopenedDoors), a chat ended
// before its first prompt (deadChatDoors), and the chats a chat starts, a Codex one among them
// (startedDoors). Each function present at both doors is played at both, against
// the same chat, so the pair's answers are read side by side.

// aloneDoors: the chat, alone in its Herdr session and mid-turn, asks who it is, lists, inspects,
// captures, resolves, saves and renames itself, reads its own transcript and history, and calls every
// function on a name no chat holds.
func aloneDoors(c *harness.Ctx, term *harness.Terminal, s *State) {
	sock, id, name := herdrSocket(*s), s.Chat.Session, s.Name
	pane := liveSeats(c, sock)[id][1]
	n := c.Unique("nosuch")
	transcript := projectTranscript(s, id)
	savedCLI, savedMCP := s.Home+"/"+c.Unique("saved-cli")+".md", s.Home+"/"+c.Unique("saved-mcp")+".md"
	c.Cleanup(func() {
		if r := c.Read("rm", "-f", savedCLI, savedMCP); r.Code != 0 {
			c.Logf("teardown: rm %s %s exited %d: %s", savedCLI, savedMCP, r.Code, r.Stderr)
		}
	})
	// pfm logs a kill, unkill or end of no chat as a failed state transition: these are the refusals the
	// cells ask for.
	for _, refused := range []string{
		`"err":"chat kill exited 4"`, `"err":"chat unkill exited 4"`, `"err":"chat end exited 4"`,
	} {
		c.Expect(refused)
	}
	// The chat's own state mid-turn is Herdr's answer, which the fake engine's turn leaves to timing:
	// working read from the transcript while Herdr knows none, or idle once Herdr says so.
	busy := statusJSON{
		"name": name, "state": "working", "idle_seconds": idleSeconds, "engine": "cc",
		"model": "claude-sonnet-4-5", "cwd": s.Project, "session_id": id, "socket": sock,
	}
	quiet := withLast(busy, "")
	quiet["state"] = "idle"
	selfStatus := func(step map[string]any, last string) door {
		return door{step: step, check: oneOf(statusIs(0, withLast(busy, last)), statusIs(0, withLast(quiet, last)))}
	}
	// The MCP door lists the chat in the state it inspects, the word `pfm chat ls` says in the same
	// turn: working or idle by Herdr's timing. The chats elsewhere and the killed ones are the other
	// branches' too, so only their being counted is read.
	selfListed := func(state string) func(answer) string {
		return jsonIs(0, "", map[string]any{
			"count": 1, "elsewhere": idleSeconds, "killed_count": idleSeconds, "matched": 1, "scope": s.Project,
			"rows": []any{map[string]any{
				"account": 1, "backend": "herdr", "dir": s.Project, "engine": "cc", "herdr": "native",
				"herdr_workspace": strings.Split(pane, ":")[0], "id": id, "kind": "live-claude", "name": name,
				"pane": pane, "project": filepath.Base(s.Project), "seat_id": s.Chat.Seat, "session": name,
				"socket": sock, "state": state,
			}},
		})
	}
	gone := "{\n  \"name\": \"" + n + "\",\n  \"state\": \"not-found\",\n  \"idle_seconds\": 0,\n  \"engine\": \"\"\n}\n" +
		"pfm chat: no chat named \"" + n + "\"\n"
	noChat := fmt.Sprintf("no chat named %q", n)
	saved := `Appended transcript \(1 user records\) \+ env snapshot -> %s \(\d+ bytes total\)`
	answers := playDoors(
		c,
		term,
		s,
		"the chat alone",
		cliIs("pfm whoami", 0, name+"\n"),
		cliIs("pfm chat whoami", 0, name+"\n"),
		mcpJSON("whoami", map[string]any{}, map[string]any{
			"engine": "cc", "id": id, "pane": pane, "session": name, "socket_name": "herdr.sock",
			"socket_path": sock, "source": "mcp-proxy", "status": "ok",
		}),
		selfStatus(cli("pfm chat status self --json"), `T Bash|{"command":"pfm chat status self --json"}`),
		selfStatus(mcpStep("status", map[string]any{"target": "self"}), `T status|{"target":"self"}`),
		door{step: cli("pfm chat ls"), check: cliRows("live chats in this repo (name · session · state · herdr · last "+
			"activity):", []string{name}, name+" "+name+" * native")},
		door{step: mcpStep("ls", map[string]any{}), check: oneOf(selfListed("working"), selfListed("idle"))},
		cliIs("pfm chat last self", 3, fmt.Sprintf("pfm chat last: %q has not answered yet\n", name)),
		mcpNotAnswered("self", name),
		cliIs("pfm chat capture self | tail -n 4 | head -n 3", 0, rule+"\n❯\n"+rule+"\n"),
		door{
			step:  mcpStep("capture", map[string]any{"target": "self", "tail_lines": 4}),
			check: captureHead(pane, sock, 4, rule, "❯", rule),
		},
		cliIs("pfm chat resolve "+name, 0, sock+"\t"+name+"\t"+id+"\n"),
		mcpJSON("resolve", map[string]any{"kind": "label", "name": name},
			map[string]any{"code": 0, "pane": pane, "socket_path": sock, "status": "ok"}),
		cliMatch("pfm chat save "+savedCLI, 0, fmt.Sprintf(saved, regexp.QuoteMeta(savedCLI))+"\n"),
		door{
			step:  mcpStep("save", map[string]any{"target": savedMCP}),
			check: receiptMatch("ok", 0, fmt.Sprintf(saved, regexp.QuoteMeta(savedMCP))),
		},
		cliIs("pfm chat name self "+name+"-x", 0, "named "+id+" -> "+name+"-x\n"),
		mcpJSON("name", map[string]any{"target": "self", "name": name},
			map[string]any{"code": 0, "message": "named " + id + " -> " + name, "status": "ok"}),
		door{step: cli("pfm chat read self --json --tail 1"), check: readTail(
			name,
			"cc",
			transcript,
			true,
			map[string]any{
				"role":  "tool",
				"tool":  "Bash",
				"input": `{"command":"pfm chat read self --json --tail 1"}`,
			},
		)},
		cliIs("pfm chat history "+id[:8]+" | head -n 1", 0, "== "+transcript+" · last 20 messages ==\n"),
		// The chat addresses itself as self through both doors: resolve and open answer as for its name.
		cliIs("pfm chat resolve self", 0, sock+"\t"+name+"\t"+id+"\n"),
		mcpJSON("resolve", map[string]any{"kind": "label", "name": "self"},
			map[string]any{"code": 0, "pane": pane, "socket_path": sock, "status": "ok"}),
		cliIs("pfm chat open self", 0, ""),
		mcpJSON("open", map[string]any{"target": "self"}, map[string]any{"code": 0, "status": "ok", "message": "live " +
			name + " on socket " + sock + " — " + name + " lives in Herdr pane " + pane + " (" + sock + ")"}),
		cliIs("pfm chat find /dev/null", 2, "pfm chat find: excerpt has no line of 20+ characters to search for\n"),
		cliIs("pfm chat status "+n+" --json", 4, gone),
		mcpJSON("status", map[string]any{"target": n}, map[string]any{
			"code": 4, "engine": "", "idle_seconds": 0, "message": noChat, "name": n, "state": "not-found",
		}),
		cliIs("pfm chat read "+n+" --json", 4, gone),
		cliIs("pfm chat last "+n, 4, notFound(n)),
		mcpJSON("last", map[string]any{"target": n},
			map[string]any{"code": 4, "message": noChat, "status": "not_found", "target": n}),
		cliIs("pfm chat capture "+n, 4, notFound(n)),
		mcpJSON(
			"capture",
			map[string]any{"target": n},
			map[string]any{"code": 4, "message": noChat, "status": "not_found"},
		),
		cliIs("pfm chat keys "+n+" Escape", 4, notFound(n)),
		mcpJSON("keys", map[string]any{"target": n, "keys": []string{"Escape"}}, map[string]any{
			"code": 4, "count": 0, "keys": []string{"Escape"}, "message": noChat, "status": "not_found",
		}),
		cliIs("pfm chat call "+n+" hi", 4, fmt.Sprintf("pfm chat: no chat named %q\n", n)),
		mcpJSON("call", map[string]any{"target": n, "message": "hi"},
			map[string]any{"code": 4, "message": noChat, "status": "not_found"}),
		cliIs("pfm chat resolve "+n, 4, notFound(n)),
		mcpJSON("resolve", map[string]any{"kind": "label", "name": n}, map[string]any{
			"candidates": fmt.Sprintf("target %q matched no live chat", n), "code": 4, "message": noChat,
			"status": "not_found",
		}),
		cliIs("pfm chat kill "+n, 4, notFound(n)),
		mcpJSON(
			"kill",
			map[string]any{"target": n},
			map[string]any{"code": 4, "message": noChat, "status": "not_found"},
		),
		cliIs("pfm chat unkill "+n, 4, notFound(n)),
		mcpJSON("unkill", map[string]any{"target": n},
			map[string]any{"code": 4, "message": noChat, "status": "not_found"}),
		cliIs("pfm chat open "+n, 4, notFound(n)),
		mcpJSON(
			"open",
			map[string]any{"target": n},
			map[string]any{"code": 4, "message": noChat, "status": "not_found"},
		),
		cliIs("pfm chat name "+n+" zz", 4, notFound(n)),
		mcpJSON("name", map[string]any{"target": n, "name": "zz"},
			map[string]any{"code": 4, "message": noChat, "status": "not_found"}),
		cliIs("pfm chat end "+n, 4, notFound(n)),
		cliIs("pfm chat watch "+n+" --once", 4, notFound(n)),
		cliIs("pfm chat ask "+n+" hi", 4, notFound(n)),
		cliIs("pfm chat stream "+n, 4, notFound(n)),
		cliIs("pfm chat modal "+n+" deny 1", 4, notFound(n)),
		cliIs("pfm chat recover "+n, 1,
			fmt.Sprintf("pfm chat recover: no rollout found for thread %q under %s/.codex/sessions\n", n, s.Home)),
	)
	// Each door's save names the size it wrote; the file holds exactly that.
	for at, path := range map[int]string{13: savedCLI, 14: savedMCP} {
		size := regexp.MustCompile(`\((\d+) bytes total\)`).FindStringSubmatch(answers[at].Text)
		if got := strings.TrimSpace(read(c, "stat", "-c", "%s", path)); len(size) != 2 || got != size[1] {
			c.Failf("%s holds %s bytes, its save answered %q", path, got, answers[at].Text)
		}
	}
}

// busyDoors: the chat inspects its child while the child's first turn holds, then calls it through the
// CLI door: pfm types the message into the working agent's composer, where it queues behind the turn
// (escape chat-mesh/call-busy-queues).
func busyDoors(c *harness.Ctx, term *harness.Terminal, s *State, hold, queued string) {
	child, id, sock := s.Mesh.ChildName, s.Mesh.Child.Session, herdrSocket(*s)
	prompt := `mock-engine: {"type":"hold","until_gone":"` + hold + `"}`
	status := statusJSON{
		"name": child, "state": "working", "idle_seconds": idleSeconds, "engine": "cc", "cwd": s.Project,
		"session_id": id, "socket": sock, "last": "U " + prompt,
	}
	playDoors(c, term, s, "the child working",
		cliStatus(child, 0, status),
		mcpStatus(child, 0, status),
		cliIs("pfm chat last "+child, 3, fmt.Sprintf("pfm chat last: %q has not answered yet\n", child)),
		mcpNotAnswered(child, child),
		cliIs("pfm chat resolve "+child, 0, sock+"\t"+child+"\t"+id+"\n"),
		mcpJSON("resolve", map[string]any{"kind": "label", "name": child},
			map[string]any{"code": 0, "pane": s.Mesh.ChildPane, "socket_path": sock, "status": "ok"}),
		door{step: cli("pfm chat read " + child + " --json --tail 2"), check: readTail(child, "cc",
			projectTranscript(s, id), false, map[string]any{"role": "user", "text": prompt})},
		// pfm proves the delivery with the pane's tail, where the agent shows the call queued.
		cliMatch("pfm chat call "+child+" "+shellQuote(directive(turnStep(queued))), 0,
			regexp.QuoteMeta(fmt.Sprintf("called LIVE into %q — Herdr raw composer confirmed", s.Mesh.ChildPane))+
				"\n"+proofOf(`(?s:.*\n)  Press up to edit queued messages\n(?s:.*)`)),
	)
}

// idleDoors: once the child's first turn is released and the call queued behind it answered, the chat
// inspects it again, captures it, sends it a key, watches and streams it, asks it and calls it through
// the CLI door, then reads the answer at both doors. It leaves the child's turn over in Herdr's eyes,
// as the call step meets it.
func idleDoors(c *harness.Ctx, term *harness.Terminal, s *State, hold, queued string) {
	child, id, sock, pane := s.Mesh.ChildName, s.Mesh.Child.Session, herdrSocket(*s), s.Mesh.ChildPane
	prompt := `mock-engine: {"type":"hold","until_gone":"` + hold + `"}`
	queuedAsk, signature := directive(turnStep(queued)), " — sid "+s.Chat.Session[:8]+" · to reply: call "+s.Name+
		" <message>"
	status := statusJSON{
		"name": child, "state": "idle", "idle_seconds": idleSeconds, "engine": "cc", "model": "claude-sonnet-4-5",
		"cwd": s.Project, "session_id": id, "socket": sock, "last": "A " + queued,
	}
	asked, called := c.Unique("asked"), c.Unique("called")
	watched := `IDLE ` + regexp.QuoteMeta(child) + ` idle_seconds=\d+\n`
	playDoors(
		c,
		term,
		s,
		"the child idle",
		cliMatch("pfm chat watch "+child+" --once", 0, watched),
		cliStatus(child, 0, status),
		mcpStatus(child, 0, status),
		cliIs("pfm chat last "+child, 0, queued+"\n"),
		mcpJSON("last", map[string]any{"target": child}, map[string]any{"target": child, "text": queued}),
		cliIs("pfm chat capture "+child+" | tail -n 4 | head -n 3", 0, rule+"\n❯\n"+rule+"\n"),
		door{
			step:  mcpStep("capture", map[string]any{"target": child, "tail_lines": 4}),
			check: captureHead(pane, sock, 4, rule, "❯", rule),
		},
		cliIs("pfm chat resolve "+child, 0, sock+"\t"+child+"\t"+id+"\n"),
		mcpJSON("resolve", map[string]any{"kind": "label", "name": child},
			map[string]any{"code": 0, "pane": pane, "socket_path": sock, "status": "ok"}),
		door{step: cli("pfm chat read " + child + " --json --tail 2"), check: readTail(child, "cc",
			projectTranscript(s, id), true, map[string]any{"role": "user", "text": queuedAsk + " " + signature},
			map[string]any{"role": "assistant", "text": queued})},
		cliIs("pfm chat keys "+child+" Escape", 0, child+"\t1 key(s)\tEscape\n"),
		mcpJSON("keys", map[string]any{"target": child, "keys": []string{"Escape"}}, map[string]any{
			"code": 0, "count": 1, "keys": []string{"Escape"}, "pane": pane, "socket_path": sock, "status": "ok",
		}),
		cliIs("pfm chat stream "+child+" --from-start --no-follow", 0, "U "+prompt+"\nA ok\nU "+queuedAsk+signature+
			"\nA "+queued+"\n"),
		cliIs("pfm chat ask --settle 1 "+child+" "+shellQuote(directive(turnStep(asked))), 0, asked+"\n"),
		door{
			step:  cli("pfm chat call " + child + " " + shellQuote(directive(turnStep(called)))),
			check: delivered(pane),
		},
		cliMatch("pfm chat watch "+child+" --once", 0, watched),
		cliIs("pfm chat last "+child, 0, called+"\n"),
		mcpJSON("last", map[string]any{"target": child}, map[string]any{"target": child, "text": called}),
	)
	// Herdr marks a turn over once its hook lands; a pane out of view whose turn ended reads done.
	c.Poll("Herdr status idle or done for "+pane, stepBound, func() (bool, string) {
		got := herdrStatus(c, *s, pane)
		return got == "idle" || got == "done", "status " + got
	})
}

// deadChildDoors: the chat inspects and captures the child the branch ended, dormant with turns,
// lists it at both doors, reopens it through the MCP door and ends it through the CLI door.
func deadChildDoors(c *harness.Ctx, term *harness.Terminal, s *State) {
	child, id := s.Mesh.ChildName, s.Mesh.Child.Session
	var answered string
	for _, entry := range entries(c, child) {
		if entry.Role == "assistant" {
			answered = entry.Text
		}
	}
	childStatus := statusJSON{
		"name": child, "state": "dead", "idle_seconds": idleSeconds, "engine": "cc", "model": "claude-sonnet-4-5",
		"cwd": s.Project, "session_id": id, "last": "U ping " + strings.TrimPrefix(unsignedFooter, "  "),
	}
	playDoors(c, term, s, "the ended child",
		cliStatus(child, 3, childStatus),
		mcpStatus(child, 3, childStatus),
		cliIs("pfm chat last "+child, 0, answered+"\n"),
		mcpJSON("last", map[string]any{"target": child}, map[string]any{"target": child, "text": answered}),
		cliIs("pfm chat capture "+child, 3, fmt.Sprintf("pfm chat capture: %q is not running\n", child)),
		mcpJSON("capture", map[string]any{"target": child},
			map[string]any{"code": 3, "message": fmt.Sprintf("%q is not running", child), "status": "dead"}),
		door{step: mcpStep("ls", map[string]any{"all": true}), check: mcpRows(map[string]string{
			child: "resume-claude resumable", s.Name: "live-claude idle|live-claude working",
		})},
		door{step: cli("pfm chat ls --all"), check: cliRows("live chats everywhere (name · session · state · herdr · "+
			"dir · last activity):", []string{s.Name, s.Mesh.PeerName, child},
			s.Name+" "+s.Name+" * native", s.Mesh.PeerName+" "+s.Mesh.PeerSock+" idle attached")},
		door{
			step: mcpStep("open", map[string]any{"target": child}),
			// Where the reopened chat runs is the placement builder's (round3-notes.md): either form passes.
			check: receiptMatch("ok", 0, "opened "+regexp.QuoteMeta(child)+` on socket \S+(?: — `+
				regexp.QuoteMeta(child)+` lives in Herdr pane w[0-9A-Za-z]+:p[0-9A-Za-z]+ \(\S+\))?`),
		},
		cliMatch("pfm chat end "+child, 0, `(end scheduled|ended) `+id+`\n`),
	)
}

// reopenedDoors: once the child deadChildDoors reopened and ended is dead, the chat reopens it through
// the CLI door and ends it again.
func reopenedDoors(c *harness.Ctx, term *harness.Terminal, s *State) {
	child := s.Mesh.ChildName
	c.Cleanup(func() {
		if r := c.Read("pfm", "chat", "status", child, "--json"); r.Code != 0 {
			return
		}
		if r := c.Read("pfm", "chat", "end", child); r.Code != 0 {
			c.Logf("teardown: pfm chat end %s exited %d: %s%s", child, r.Code, r.Stderr, r.Stdout)
		}
	})
	ended := `(end scheduled|ended) ` + s.Mesh.Child.Session + `\n`
	notLive(c, child)
	playDoors(c, term, s, "the child reopened again",
		cliIs("pfm chat open "+child, 0, ""),
		cliMatch("pfm chat end "+child, 0, ended),
	)
	notLive(c, child)
}

// deadChatDoors: the chat inspects and calls the chat the owner ended before its first prompt, kills,
// unkills and lists it at both doors.
func deadChatDoors(c *harness.Ctx, term *harness.Terminal, s *State, dead, deadID string) {
	deadStatus := statusJSON{
		"name": dead, "state": "dead", "idle_seconds": idleSeconds, "engine": "cc", "session_id": deadID,
	}
	revive := fmt.Sprintf("%q is dead (engine cc, session %s); revive it with: pfm chat open %s", dead, deadID, dead)
	playDoors(c, term, s, "the chat ended unprompted",
		cliStatus(dead, 3, deadStatus),
		mcpStatus(dead, 3, deadStatus),
		cliIs("pfm chat last "+dead, 3, fmt.Sprintf("pfm chat last: %q has not answered yet\n", dead)),
		mcpNotAnswered(dead, dead),
		cliIs("pfm chat call "+dead+" hi", 3, revive+"\n"),
		mcpJSON("call", map[string]any{"target": dead, "message": "hi"},
			map[string]any{"code": 3, "message": revive, "status": "dead"}),
		cliIs("pfm chat kill "+dead, 0, "killed "+deadID+"\tde-listed only, no live pane closed\n"),
		mcpJSON("unkill", map[string]any{"target": dead},
			map[string]any{"code": 0, "message": "unkilled " + deadID, "status": "ok"}),
		mcpJSON("kill", map[string]any{"target": dead}, map[string]any{
			"code": 0, "message": "killed " + deadID + "\tde-listed only, no live pane closed", "status": "ok",
		}),
		cliIs("pfm chat unkill "+dead, 0, "unkilled "+deadID+"\n"),
		door{step: mcpStep("ls", map[string]any{"all": true}), check: mcpRows(map[string]string{
			dead: "resume-claude resumable", s.Name: "live-claude idle|live-claude working",
		})},
		door{step: cli("pfm chat ls --all"), check: cliRows("live chats everywhere (name · session · state · herdr · "+
			"dir · last activity):", []string{s.Name, dead}, s.Name+" "+s.Name+" * native")},
	)
}

// startedDoors: the chat starts two Claude chats and a Codex chat through the CLI door; then it plays
// every function on the Codex chat at both doors, kills one Claude chat through the MCP door, and has
// the other exit itself through the MCP door. Each kill that says it closed a pane leaves neither
// that pane nor the chat's engine running (escape chat-mesh/kill-closes-pane).
func startedDoors(c *harness.Ctx, term *harness.Terminal, s *State) {
	sock := herdrSocket(*s)
	claude, codex, victim := c.Unique("started"), c.Unique("cx"), c.Unique("victim")
	c.Cleanup(func() {
		for _, name := range []string{claude, codex, victim} {
			if r := c.Read("pfm", "chat", "status", name, "--json"); r.Code != 0 {
				continue
			}
			if r := c.Read("pfm", "chat", "end", name); r.Code != 0 {
				c.Logf("teardown: pfm chat end %s exited %d: %s%s", name, r.Code, r.Stderr, r.Stdout)
			}
		}
	})
	born := `%s\t%s\therdr\t` + regexp.QuoteMeta(sock) + `\tw[0-9A-Za-z]+:p[0-9A-Za-z]+\tstarting\n`
	playDoors(c, term, s, "the chats it starts",
		cliMatch("pfm chat new --engine cc --name "+claude, 0, fmt.Sprintf(born, "cc", claude)),
		cliMatch("pfm chat new --engine cx --name "+codex, 0, fmt.Sprintf(born, "cx", codex)),
		cliMatch("pfm chat new --engine cc --name "+victim, 0, fmt.Sprintf(born, "cc", victim)),
	)
	ids := map[string]string{}
	c.Poll(claude+", "+codex+" and "+victim+" idle", stepBound, func() (bool, string) {
		var seen []string
		for _, name := range []string{claude, codex, victim} {
			r := c.Read("pfm", "chat", "status", name, "--json")
			var status struct {
				State   string `json:"state"`
				Session string `json:"session_id"`
			}
			if json.Unmarshal([]byte(r.Stdout), &status) != nil || status.State != "idle" {
				return false, r.Stdout + r.Stderr
			}
			ids[name] = status.Session
			seen = append(seen, r.Stdout)
		}
		return true, strings.Join(seen, "")
	})
	statsRows(c, s.Name, claude, codex, victim)
	seats := liveSeats(c, sock)
	cxID, cxPane, claudePane := ids[codex], seats[ids[codex]][1], seats[ids[claude]][1]
	victimID, victimPane := ids[victim], seats[ids[victim]][1]
	claimant := func(id, pane string) string {
		return fmt.Sprintf("thread id %s (socket %s, pane %s, name %q)", id, sock, pane, victim)
	}
	claimants := []string{claimant(cxID, cxPane), claimant(victimID, victimPane)}
	slices.Sort(claimants)
	twins := fmt.Sprintf("%q matches 2 chats in the roster; address by thread id:\n  %s", victim,
		strings.Join(claimants, "\n  "))
	recovered := s.Home + "/.codex/recovered-" + cxID
	c.Cleanup(func() {
		if r := c.Read("rm", "-rf", recovered); r.Code != 0 {
			c.Logf("teardown: rm -rf %s exited %d: %s", recovered, r.Code, r.Stderr)
		}
	})
	cxStatus := statusJSON{
		"name": codex, "state": "idle", "idle_seconds": idleSeconds, "engine": "cx", "model": "codex-fixture-model",
		"cwd": s.Project, "session_id": cxID, "socket": sock,
	}
	asked, called := c.Unique("cxasked"), c.Unique("cxcalled")
	excerpt := "/tmp/" + asked
	c.Cleanup(func() {
		if r := c.Read("rm", "-f", excerpt); r.Code != 0 {
			c.Logf("teardown: rm -f %s exited %d: %s", excerpt, r.Code, r.Stderr)
		}
	})
	c.Expect(`"err":"chat end exited 3"`) // the end of the Codex chat its kill closed
	playDoors(c, term, s, "a Codex chat",
		cliStatus(codex, 0, cxStatus),
		mcpStatus(codex, 0, cxStatus),
		cliIs("pfm chat last "+codex, 3, fmt.Sprintf("pfm chat last: %q has not answered yet\n", codex)),
		mcpNotAnswered(codex, codex),
		cliIs("pfm chat capture "+codex+" | tail -n 2", 0, "› Ask Codex to do anything\ncodex-fixture-model · "+
			s.Project+"\n"),
		door{
			step: mcpStep("capture", map[string]any{"target": codex, "tail_lines": 3}),
			check: captureHead(cxPane, sock, 3, "", "› Ask Codex to do anything",
				"codex-fixture-model · "+s.Project),
		},
		cliIs("pfm chat resolve "+codex, 0, sock+"\t"+codex+"\t"+cxID+"\n"),
		mcpJSON("resolve", map[string]any{"kind": "label", "name": codex},
			map[string]any{"code": 0, "pane": cxPane, "socket_path": sock, "status": "ok"}),
		cliMatch("pfm chat read "+codex+" --json --tail 2", 0, `\{\n  "engine": "cx",\n  "entries": \[\],\n  "name": "`+
			codex+`",\n  "path": "`+regexp.QuoteMeta(s.Home)+`/\.codex/sessions/\d{4}/\d\d/\d\d/rollout-[\dT-]+-`+cxID+
			`\.jsonl",\n  "truncated": false\n\}\n`),
		cliIs("pfm chat keys "+codex+" Escape", 0, codex+"\t1 key(s)\tEscape\n"),
		mcpJSON("keys", map[string]any{"target": codex, "keys": []string{"Escape"}}, map[string]any{
			"code": 0, "count": 1, "keys": []string{"Escape"}, "pane": cxPane, "socket_path": sock, "status": "ok",
		}),
		cliIs("pfm chat ask --settle 1 "+codex+" "+shellQuote(directive(turnStep(asked))), 0, asked+"\n"),
		door{
			step: mcpStep("call", map[string]any{"target": codex, "message": directive(turnStep(called))}),
			check: receiptMatch("delivered", 0,
				regexp.QuoteMeta(fmt.Sprintf("called LIVE into %q — Herdr raw composer confirmed", cxPane))),
		},
		cliMatch("pfm chat watch "+codex+" --once", 0, `IDLE `+codex+` idle_seconds=\d+\n`),
		cliIs("pfm chat last "+codex, 0, called+"\n"),
		mcpJSON("last", map[string]any{"target": codex}, map[string]any{"target": codex, "text": called}),
		cliIs("pfm chat stream "+codex+" --from-start --no-follow", 0, fmt.Sprintf("U %[1]s — sid %[3]s · to reply: "+
			"call %[4]s <message>\nA %[5]s\nU %[2]s — sid %[3]s · to reply: call %[4]s <message>\nA %[6]s\n",
			directive(turnStep(asked)), directive(turnStep(called)), s.Chat.Session[:8], s.Name, asked, called)),
		cliMatch("pfm chat history "+cxID[:8]+" | head -n 1", 0, "== "+regexp.QuoteMeta(s.Home)+
			`/\.codex/sessions/\d{4}/\d\d/\d\d/rollout-[\dT-]+-`+cxID+`\.jsonl · last 20 messages ==\n`),
		// pfm chat find searches Claude transcripts only, so the Codex chat's own excerpt is refused.
		cliIs("printf '%s\\n' "+shellQuote(directive(turnStep(asked)))+" > "+excerpt+" && pfm chat find "+excerpt, 2,
			"pfm chat find: no session contains the excerpt in the Claude transcripts searched (Codex and OpenCode "+
				"sessions are not searched); try a longer or more distinctive chunk\n"),
		cliIs("pfm chat modal "+codex+" deny 1", 0, "modal denied on "+codex+"\n"),
		// The schedule table is machine-wide (other branches arm their own), so the cell counts the rows of the id
		// it just created.
		cliMatch("id=$(pfm chat schedule set --every 1h --prompt ping "+codex+") && pfm chat schedule ls | "+
			`awk -v id="$id" '$1 == id { n++ } END { print n " listed" }' && pfm chat schedule rm "$id"`, 0,
			`1 listed\nremoved schedule \d+\n`),
		cliIs("pfm chat open "+codex, 0, ""),
		mcpJSON("open", map[string]any{"target": codex}, map[string]any{"code": 0, "status": "ok", "message": "live " +
			codex + " on socket " + sock + " — " + codex + " lives in Herdr pane " + cxPane + " (" + sock + ")"}),
		// Named as the victim, two chats answer one name: resolve refuses it alike at both doors.
		cliIs("pfm chat name "+codex+" "+victim, 0, "named "+cxID+" -> "+victim+"\n"),
		cliIs("pfm chat resolve "+victim, 2, "pfm chat: "+twins+"\n"),
		mcpJSON("resolve", map[string]any{"kind": "label", "name": victim},
			map[string]any{"candidates": twins, "code": 2, "message": twins, "status": "ambiguous"}),
		mcpJSON("name", map[string]any{"target": cxID, "name": codex},
			map[string]any{"code": 0, "message": "named " + cxID + " -> " + codex, "status": "ok"}),
		cliIs("pfm chat recover "+cxID, 0, fmt.Sprintf("messages=4 carried=0 malformed=0\nrecovered thread %[2]s\n"+
			"  %[1]s/brief.md\n  %[1]s/compaction-memory.md\n  %[1]s/transcript.md\n\nBrief a replacement seat with:\n"+
			"  pfm chat call <socket> 'RECOVERY: read %[1]s/brief.md, then compaction-memory.md, then the end of "+
			"transcript.md.'\n", recovered, cxID)),
		cliMatch("pfm chat kill "+codex, 0, `killed `+cxID+`\tclosing pane `+cxPane+` on socket \S+\n`),
		mcpJSON("unkill", map[string]any{"target": codex},
			map[string]any{"code": 0, "message": "unkilled " + cxID, "status": "ok"}),
		mcpJSON("kill", map[string]any{"target": codex}, map[string]any{
			"code": 0, "message": "killed " + cxID + "\tde-listed only, no live pane closed", "status": "ok",
		}),
		cliIs("pfm chat unkill "+codex, 0, "unkilled "+cxID+"\n"),
		cliIs("pfm chat end "+codex, 3, fmt.Sprintf("pfm chat end: %q is not running\n", codex)),
		door{
			step: mcpStep("kill", map[string]any{"target": victim}),
			check: receiptMatch("ok", 0, "killed "+victimID+"\tclosing pane "+regexp.QuoteMeta(victimPane)+
				` on socket \S+`),
		},
		door{
			step:  cli("pfm chat call " + claude + " " + shellQuote(directive(mcpStep("exit", map[string]any{})))),
			check: delivered(claudePane),
		},
	)
	notLive(c, claude)
	c.Poll("the panes and engines of the killed "+codex+" and "+victim+" gone", stepBound, func() (bool, string) {
		panes, table := herdrPanes(c, *s), processes(c)
		var alive []string
		for _, pane := range []string{cxPane, victimPane} {
			if _, open := panes[pane]; open {
				alive = append(alive, "pane "+pane)
			}
		}
		for _, p := range table {
			if p.Session == cxID || p.Session == victimID {
				alive = append(alive, fmt.Sprintf("engine %d of session %s", p.Pid, p.Session))
			}
		}
		return len(alive) == 0, strings.Join(alive, ", ")
	})
	exit := regexp.MustCompile(
		`^exit scheduled: killed [0-9a-f-]{36}\tclosing pane w[0-9A-Za-z]+:p[0-9A-Za-z]+ on socket [0-9a-f]{32} — ` +
			`reply one line and end this turn; the pane closes once the turn is over$`,
	)
	if got := mcpReceipt(
		c,
		claude,
		ids[claude],
	); got.Status != "ok" || got.Code != 0 ||
		!exit.MatchString(got.Message) {
		c.Failf("%s's MCP exit = %+v, want status ok, code 0, message matching %s", claude, got, exit)
	}
}

// projectTranscript is the path of s's project's Claude transcript of session id.
func projectTranscript(s *State, id string) string {
	return s.Home + "/.claude/projects/" + strings.ReplaceAll(s.Project, "/", "-") + "/" + id + ".jsonl"
}

// oneOf is a check that the answer passes one of checks; it names what each wanted.
func oneOf(checks ...func(answer) string) func(answer) string {
	return func(got answer) string {
		var problems []string
		for _, check := range checks {
			problem := check(got)
			if problem == "" {
				return ""
			}
			problems = append(problems, problem)
		}
		return strings.Join(problems, "; or ")
	}
}

// delivered is a check that the CLI door's call typed its message live into Herdr pane.
func delivered(pane string) func(answer) string {
	return func(got answer) string {
		pattern := herdrDelivered(pane)
		body, found := strings.CutSuffix(got.Text, "\n")
		if got.Code != 0 || !found || !pattern.MatchString(body) {
			return fmt.Sprintf("want code 0, text matching %s then a newline", pattern)
		}
		return ""
	}
}

// receiptMatch is a check that the MCP door answered a receipt of status and code whose message the
// pattern matches whole.
func receiptMatch(status string, code int, pattern string) func(answer) string {
	compiled := regexp.MustCompile("^(?:" + pattern + ")$")
	return func(got answer) string {
		var decoded receipt
		err := json.Unmarshal([]byte(got.Text), &decoded)
		if err != nil || got.Code != 0 || decoded.Status != status || decoded.Code != code ||
			!compiled.MatchString(decoded.Message) {
			return fmt.Sprintf("want status %s, code %d, message matching %s (%v)", status, code, compiled, err)
		}
		return ""
	}
}

// cliRows is a check that `pfm chat ls` answered header and, of the chats named among, exactly rows,
// each its name, session, state and Herdr columns joined by a space ("*" matches any one). Rows of
// chats another step owns are not read; a name among that lists no row is the absence it asserts.
func cliRows(header string, among []string, rows ...string) func(answer) string {
	return func(got answer) string {
		lines := strings.Split(strings.TrimSuffix(got.Text, "\n"), "\n")
		var seen []string
		for _, line := range lines[1:] {
			if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "(") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 4 {
				return fmt.Sprintf("row %q holds no four columns", line)
			}
			if slices.Contains(among, fields[0]) {
				seen = append(seen, strings.Join(fields[:4], " "))
			}
		}
		matched := len(seen) == len(rows) && got.Code == 0 && lines[0] == header
		for index := 0; matched && index < len(rows); index++ {
			want := strings.Fields(rows[index])
			for column, field := range strings.Fields(seen[index]) {
				matched = matched && (want[column] == "*" || want[column] == field)
			}
		}
		if !matched {
			return fmt.Sprintf("want code 0, header %q, rows %q among the chats %q; rows read %q", header, rows,
				among, seen)
		}
		return ""
	}
}

// mcpRows is a check that MCP ls answered, for each name want holds, one row of that kind and state;
// "|" parts a want's alternatives.
func mcpRows(want map[string]string) func(answer) string {
	return func(got answer) string {
		var listed struct {
			Rows []struct {
				Name  string `json:"name"`
				Kind  string `json:"kind"`
				State string `json:"state"`
			} `json:"rows"`
		}
		if err := json.Unmarshal([]byte(got.Text), &listed); err != nil || got.Code != 0 {
			return fmt.Sprintf("want code 0 and a JSON listing (%v)", err)
		}
		seen := map[string]string{}
		for _, row := range listed.Rows {
			if _, wanted := want[row.Name]; wanted {
				seen[row.Name] += row.Kind + " " + row.State
			}
		}
		for name, row := range want {
			if !slices.Contains(strings.Split(row, "|"), seen[name]) {
				return fmt.Sprintf("want rows %v, read %v", want, seen)
			}
		}
		return ""
	}
}
