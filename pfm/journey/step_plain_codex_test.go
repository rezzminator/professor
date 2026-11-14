//go:build journey

package journey

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// plainCodex: in a plain terminal the owner goes to the demo project and types cx, pfm's command for
// a Codex chat in a tmux server of its own; the fake Codex's composer comes up, and pfm lists it as the
// one live Codex chat on that socket, run by one engine. From a second terminal he reloads it with a
// follow-up (`pfm chat reload --then`): a new Codex resumes the thread in the same terminal, and the
// follow-up reaches it once and is answered. When the branch's subtree ends, failed or not, the chat
// is ended and no engine of it is left.
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
	reply := c.Unique("then-pong")
	then := "the reborn thread answers this follow-up " + directive(turnStep(reply))
	cli := c.Terminal(s.Term + "-cli")
	cli.WaitFor(prompt)
	if command := "pfm chat reload --sock " + socket + " --then '" + then + "'"; shellRun(cli, "reload", command,
		20*time.Second) != 0 {
		c.Failf("%s exited non-zero, want 0", command)
	}
	c.Poll("a new Codex engine for thread "+s.Chat.Session, 20*time.Second, func() (bool, string) {
		pid := liveEngine(c, s.Chat.Session)
		return pid != 0 && pid != s.Chat.Engine, fmt.Sprintf("engine %d, before %d", pid, s.Chat.Engine)
	})
	c.Poll("pfm's reload run on "+socket+" done", 20*time.Second, func() (bool, string) {
		run := reloadRun(c, socket)
		return run == "", run
	})
	c.Poll("the --then text once among thread "+s.Chat.Session+"'s prompts", 20*time.Second, func() (bool, string) {
		got := codexPrompts(c, s.Chat.Session)
		n := len(slices.DeleteFunc(slices.Clone(got), func(p string) bool { return p != then }))
		return n == 1, fmt.Sprintf("prompts %q", got)
	})
	term.WaitFor("⏺ "+reply, 20*time.Second)
	s.Chat.Engine = liveEngine(c, s.Chat.Session)
	return s
}

// codexPrompts is every user message of the Codex thread's rollout, in order.
func codexPrompts(c *harness.Ctx, thread string) []string {
	out := read(c, "sh", "-c", `for f in "$HOME"/.codex/sessions/*/*/*/rollout-*-"$1".jsonl; `+
		`do [ -f "$f" ] && readlink -f "$f"; done | sort -u`, "sh", thread)
	paths := strings.Fields(out)
	if len(paths) != 1 {
		c.Failf("thread %s has rollouts %q, want one", thread, paths)
	}
	var texts []string
	for _, line := range strings.Split(read(c, "cat", paths[0]), "\n") {
		var record struct {
			Payload struct {
				Type    string `json:"type"`
				Role    string `json:"role"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"payload"`
		}
		if json.Unmarshal([]byte(line), &record) != nil || record.Payload.Type != "message" ||
			record.Payload.Role != "user" {
			continue
		}
		for _, part := range record.Payload.Content {
			if part.Type == "input_text" {
				texts = append(texts, part.Text)
			}
		}
	}
	return texts
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
