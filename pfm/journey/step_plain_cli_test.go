//go:build journey

package journey

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// plainChatNew: in a plain terminal the owner starts chats with `pfm chat new` and no picker. First from
// a shell whose PATH has lost his ~/.local/bin, as a service's does: pfm refuses, naming the engine
// binary it cannot reach, and no chat, seat or tmux server comes of it. Then in a fresh directory, on a
// Claude that holds it at the folder-trust dialog: pfm refuses naming the dialog and presses no key on
// it, and `pfm chat end` takes the held chat back. Then with --await and a brief past tmux's 16 KiB
// message cap ending in a turn for the fake engine: pfm prints the turn's reply, the transcript's first
// prompt is the whole brief.
func plainChatNew(c *harness.Ctx, s State) State {
	term := c.Terminal("plain-chat-new")
	// Its own project: plain-claude reads demo's git state while this runs.
	project := ownerHome + "/work/" + c.Unique("chat-new")
	c.Cleanup(func() { c.Read("rm", "-rf", project) })
	term.WaitFor(prompt)
	var names []string
	c.Cleanup(func() {
		for _, name := range names {
			if len(seatsWhere(c, "state = 'live' and name = '"+name+"'")) == 0 {
				continue
			}
			if r := c.Read("pfm", "chat", "end", name); r.Code != 0 {
				c.Logf("teardown: pfm chat end %s exited %d: %s", name, r.Code, r.Stderr)
			}
		}
	})

	lost := c.Unique("no-engine")
	out, errs, code := typedRun(
		c,
		term,
		"no-engine",
		"mkdir -p "+project+" && cd "+project+` && PATH="$(dirname "$(command -v tmux)"):/usr/bin:/bin" `+
			"~/.local/bin/pfm chat new --engine cc --name "+lost,
	)
	if want := `engine binary "claude" is not reachable from this process's PATH`; code == 0 ||
		!strings.Contains(errs, want) {
		c.Failf("pfm chat new without ~/.local/bin on PATH: exit %d, stdout %q, stderr %q; want a refusal naming %q",
			code, out, errs, want)
	}
	if seats := seatsWhere(c, "name = '"+lost+"'"); len(seats) != 0 {
		c.Failf("seats %v named %s after the refusal, want none", seats, lost)
	}
	for _, p := range processes(c) {
		if strings.Contains(p.Args, lost) {
			c.Failf("process %d %q runs for the refused chat %s", p.Pid, p.Args, lost)
		}
	}

	held, fresh := c.Unique("trust-held"), c.Unique("fresh")
	names = append(names, held)
	scenario := ownerHome + "/" + fresh + ".json"
	// The machine's ~/.zshenv names one scenario for every engine; the chat's own zsh reads this one instead.
	zdot := ownerHome + "/" + fresh + ".zdot"
	out, errs, code = typedRun(c, term, "trust-held", `sed 's/^{/{"trust_dialog":true,/' "$MOCK_ENGINE_SCENARIO" >`+
		scenario+" && mkdir -p "+zdot+" ~/work/"+fresh+` && printf '. ~/.zshenv\nexport MOCK_ENGINE_SCENARIO=%s\n' `+
		scenario+" >"+zdot+"/.zshenv && cd ~/work/"+fresh+" && ZDOTDIR="+zdot+
		" pfm chat new --engine cc --name "+held+" hi")
	if code == 0 || !strings.Contains(errs, "folder-trust dialog") {
		c.Failf("pfm chat new on a Claude held at the folder-trust dialog: exit %d, stdout %q, stderr %q;"+
			" want a refusal naming the folder-trust dialog", code, out, errs)
	}
	heldSeat := seatsWhere(c, "state = 'live' and name = '"+held+"'")
	var heldSession string
	for session := range heldSeat {
		heldSession = session
	}
	if len(heldSeat) != 1 {
		c.Failf("live seats named %s = %v, want the one held at the dialog", held, heldSeat)
	} else {
		pane := read(c, "tmux", "-S", seatServer(c, State{Chat: Chat{Session: heldSession}}), "capture-pane", "-p")
		if !strings.Contains(pane, "❯ 1. Yes, I trust this folder") {
			c.Failf("the held chat's pane no longer shows the dialog's first choice:\n%s", pane)
		}
		prompts := read(c, "sh", "-c", `cat "$HOME"/.cc/*/projects/*/"$1".jsonl 2>/dev/null | `+
			`grep -c '"type":"user"' || true`, "sh", heldSession)
		if strings.TrimSpace(prompts) != "0" {
			c.Failf("the held chat's transcript has %s user records, want none", strings.TrimSpace(prompts))
		}
	}
	c.Expect(`"cause":"pfm chat new","dur_ms":0,"err":"chat new exited 1"`)
	for _, line := range strings.Split(read(c, "pfm", "ls", "--all", "--tsv"), "\n") {
		if strings.Contains(line, held) {
			c.Logf("pfm ls --all row of the held chat: %s", line)
		}
	}
	if r := c.Read("pfm", "chat", "end", held); r.Code != 0 {
		c.Failf("pfm chat end %s exited %d: %s", held, r.Code, r.Stderr)
	}
	if heldSession != "" {
		chatEnded(c, heldSession, 20*time.Second)
	}

	long, reply := c.Unique("long-brief"), c.Unique("await-reply")
	names = append(names, long)
	brief := ownerHome + "/" + long + ".txt"
	text := strings.Repeat("the brief goes on past tmux's message cap. ", 400) +
		`mock-engine: {"type":"turn","reply":"` + reply + `"}`
	read(c, "sh", "-c", `printf %s "$2" >"$1"`, "sh", brief, text)
	out, errs, code = typedRunWithin(c, term, "await-long", "cd "+project+" && pfm chat new --engine cc --name "+long+
		` --await "$(cat `+brief+`)"`, 60*time.Second)
	if code != 0 || out != reply+"\n" {
		c.Failf("pfm chat new --await with a %d-byte brief: exit %d, stdout %q, stderr %q; want exit 0 printing %q",
			len(text), code, out, errs, reply+"\n")
	}
	session := ""
	for id := range seatsWhere(c, "state = 'live' and name = '"+long+"'") {
		session = id
	}
	if records := userTexts(
		c,
		transcriptOf(c, State{Chat: Chat{Session: session}}),
	); len(records) == 0 ||
		records[0] != text {
		first := "(none)"
		if len(records) > 0 {
			first = records[0]
		}
		c.Failf("chat %s (%s): its transcript's first user record is %d bytes, want the %d-byte brief; it starts %.80q",
			long, session, len(first), len(text), first)
	}
	if r := c.Read("pfm", "chat", "end", long); r.Code != 0 {
		c.Failf("pfm chat end %s exited %d: %s", long, r.Code, r.Stderr)
	}
	if session != "" {
		chatEnded(c, session, 20*time.Second)
	}
	return s
}

// nestedTmux: in a plain terminal the owner starts a tmux server of his own and, inside it, types
// claude with a settings file of his own; the fake Claude's box comes up, its engine carrying --settings
// once: pfm's own, his file's keys merged in. Then he kills his tmux server from outside: within 10 s no live row, seat, engine
// or tmux process of the chat is left.
func nestedTmux(c *harness.Ctx, s State) State {
	outer, mine := c.Unique("outer"), ownerHome+"/"+c.Unique("mine")+".json"
	read(c, "sh", "-c", `printf '{"env":{"OWNER_OWN":"1"}}\n' >"$1"`, "sh", mine)
	term := c.Terminal("plain-nested")
	project := ownerHome + "/work/" + c.Unique("nested")
	c.Cleanup(func() { c.Read("rm", "-rf", project) })
	term.WaitFor(prompt)
	var session string
	c.Cleanup(func() {
		if r := c.Read("tmux", "-L", outer, "kill-server"); r.Code != 0 {
			c.Logf("teardown: tmux -L %s kill-server exited %d: %s", outer, r.Code, r.Stderr)
		}
		if session != "" {
			if rows, _ := liveChats(c, ""); rows[session] != [2]string{} {
				c.Read("pfm", "chat", "end", session)
			}
		}
	})
	term.Type("tmux -L " + outer + " new -s outer\r")
	term.WaitFor(prompt)
	term.Type("mkdir -p " + project + " && cd " + project + " && claude --settings " + mine + "\r")
	term.WaitFor("❯", 20*time.Second)
	var engine int
	c.Poll("one Claude engine carrying the owner's settings "+mine, stepBound, func() (bool, string) {
		var found []string
		for pid, p := range processes(c) {
			if p.Engine == "claude" && (strings.Contains(p.Args, mine) || strings.Contains(p.Args, `"OWNER_OWN":"1"`)) {
				found = append(found, fmt.Sprintf("%d %s", pid, p.Session))
				engine, session = pid, p.Session
			}
		}
		return len(found) == 1 && session != "", fmt.Sprintf("engines %v", found)
	})
	c.Poll("chat "+session+" live on a live seat", stepBound, func() (bool, string) {
		rows, _ := liveChats(c, "")
		seats := seatsWhere(c, "state = 'live' and session_id = '"+session+"'")
		return rows[session][0] == "live-claude" &&
				len(seats) == 1, fmt.Sprintf(
				"row %v, seats %v",
				rows[session],
				seats,
			)
	})
	argv, err := procTable(c).Cmdline(engine)
	if err != nil {
		c.Failf("read engine %d's argv: %v", engine, err)
	}
	var settings []string
	for i, word := range argv {
		if word == "--settings" && i+1 < len(argv) {
			settings = append(settings, argv[i+1])
		} else if v, ok := strings.CutPrefix(word, "--settings="); ok {
			settings = append(settings, v)
		}
	}
	var merged struct {
		Env   map[string]string `json:"env"`
		Hooks map[string]any    `json:"hooks"`
	}
	if len(settings) != 1 || json.Unmarshal([]byte(settings[0]), &merged) != nil || merged.Env["OWNER_OWN"] != "1" ||
		merged.Hooks["SessionStart"] == nil {
		c.Failf("engine %d carries --settings %q, want once, pfm's settings with the owner's %s merged in"+
			" (env OWNER_OWN=1 beside pfm's SessionStart hooks)", engine, settings, mine)
	}
	if r := c.Read("tmux", "-L", outer, "kill-server"); r.Code != 0 {
		c.Failf("tmux -L %s kill-server exited %d: %s", outer, r.Code, r.Stderr)
	}
	c.Poll("chat "+session+" gone with the owner's tmux server", 10*time.Second, func() (bool, string) {
		rows, _ := liveChats(c, "")
		seats := seatsWhere(c, "state = 'live' and session_id = '"+session+"'")
		var left []string
		for pid, p := range processes(c) {
			if p.Session == session || strings.Contains(p.Args, outer) {
				left = append(left, fmt.Sprintf("%d %s", pid, p.Args))
			}
		}
		_, listed := rows[session]
		return !listed && len(seats) == 0 && len(left) == 0,
			fmt.Sprintf("listed %t, seats %v, processes %v", listed, seats, left)
	})
	return s
}

// typedRunWithin is typedRun for a command that may take longer than a step's bound.
func typedRunWithin(
	c *harness.Ctx, term *harness.Terminal, label, command string, within time.Duration,
) (stdout, stderr string, code int) {
	base := ownerHome + "/" + c.Unique(label)
	code = shellRun(term, label, "{ "+command+"; } >"+base+".out 2>"+base+".err", within)
	stdout, stderr = read(c, "cat", base+".out"), read(c, "cat", base+".err")
	if r := c.Read("rm", base+".out", base+".err"); r.Code != 0 {
		c.Logf("rm %s.{out,err} exited %d: %s", base, r.Code, r.Stderr)
	}
	return stdout, stderr, code
}
