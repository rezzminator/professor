//go:build journey

package journey

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// tmuxStopped: beside the restart chain's two live Herdr chats the owner starts a tmux-hosted chat
// (`pfm chat new --name {held}`) and stops its tmux server (kill -STOP). The picker still draws within
// its bound, both healthy chats counted live, and once left prints its warning about the stopped
// server naming kill -CONT; claude typed in a plain terminal still launches, in tmux (no Herdr server
// runs), its placement record naming the server that did not answer. After kill -CONT no tmux client
// is left hanging and the typed chat is the one live chat on its socket.
func tmuxStopped(c *harness.Ctx, s State) State {
	held := c.Unique("held")
	term := c.Terminal("tmux-stopped")
	term.WaitFor(prompt)
	command := "mkdir -p ~/work/" + held + " && cd ~/work/" + held + " && pfm chat new --name " + held + " hello"
	if code := shellRun(term, "new-"+held, command, 30*time.Second); code != 0 {
		c.Failf("%s exited %d:\n%s", command, code, term.Screen())
	}
	c.Cleanup(func() {
		if r := c.Read("pfm", "chat", "end", held); r.Code != 0 {
			c.Logf("teardown: pfm chat end %s exited %d: %s%s", held, r.Code, r.Stdout, r.Stderr)
		}
	})
	server, socket := 0, ""
	c.Poll("the tmux server of "+held, stepBound, func() (bool, string) {
		for pid, p := range processes(c) {
			if f := strings.Fields(p.Args); len(f) > 3 && filepath.Base(f[0]) == "tmux" && f[1] == "-S" &&
				f[3] == "new-session" && strings.Contains(p.Args, " -n "+held+" ") {
				server, socket = pid, f[2]
			}
		}
		return server != 0, "no tmux server names its window " + held
	})
	c.Cleanup(func() {
		if r := c.Read("kill", "-CONT", fmt.Sprint(server)); r.Code != 0 {
			c.Logf("teardown: kill -CONT %d exited %d: %s", server, r.Code, r.Stderr)
		}
	})
	if code := shellRun(term, "stop-"+held, fmt.Sprintf("clear && kill -STOP %d", server), 10*time.Second); code != 0 {
		c.Failf("kill -STOP %d exited %d", server, code)
	}
	term.Type("clear && pfm\r")
	term.WaitFor("New chat", 10*time.Second)
	term.WaitFor(fmt.Sprintf("● %d Claude", len(s.Chats)), 10*time.Second)
	// The picker opens on the cached fleet; its warnings come from its own probe, the first refresh.
	term.WaitScreen("the picker's first refresh landed", 20*time.Second, func(screen string) bool {
		return !strings.Contains(screen, "⟳ refreshing")
	})
	term.Press(harness.Esc)
	want := "pfm: tmux probe warning: " + filepath.Base(socket) + ": could not read this tmux server: "
	term.WaitScreen("the picker's warning naming kill -CONT", 10*time.Second, func(screen string) bool {
		flat := unwrapped(screen)
		return strings.Contains(flat, want) && strings.Contains(flat, ": resume it with kill -CONT {pid}")
	})
	// The caller read fails on the stopped server; with no Herdr server running the terminal's own
	// chat is still born, in tmux, and the placement record names the read that failed.
	typed := State{Home: s.Home, Term: "tmux-stopped-claude", Project: filepath.Join(s.Home, "work", "demo")}
	launched := c.Terminal(typed.Term)
	launched.WaitFor(prompt)
	c.Cleanup(func() { stopPlainChat(c, &typed, launched) })
	launched.Type("cd $HOME/work/demo && claude\r")
	launched.WaitFor("❯", 20*time.Second)
	own := attached(c, launched)
	wantErr := "look up the caller: read tmux panes on " + socket + ": tmux server did not answer within 2s: " + socket
	c.Poll("the placement record of claude typed beside the stopped server", stepBound, func() (bool, string) {
		from, to := c.Window()
		var seen []string
		for _, record := range activity(c, from, to) {
			var placement struct {
				Msg, Door, Chosen string
				CallerErr         string `json:"caller_err"`
			}
			if json.Unmarshal([]byte(record), &placement) != nil || placement.Msg != "chat.placement" ||
				placement.Door != "hook.launch" {
				continue
			}
			if placement.Chosen == "tmux" && placement.CallerErr == wantErr {
				return true, ""
			}
			seen = append(seen, fmt.Sprintf("chosen %q caller_err %q", placement.Chosen, placement.CallerErr))
		}
		return false, fmt.Sprintf("want chosen \"tmux\" caller_err %q; hook.launch placements: %v", wantErr, seen)
	})
	if code := shellRun(term, "cont-"+held, fmt.Sprintf("clear && kill -CONT %d", server), 10*time.Second); code != 0 {
		c.Failf("kill -CONT %d exited %d", server, code)
	}
	c.Poll("no tmux client left hanging", stepBound, func() (bool, string) {
		var clients []string
		for pid, p := range processes(c) {
			if f := strings.Fields(p.Args); len(f) > 1 && filepath.Base(f[0]) == "tmux" &&
				!strings.Contains(p.Args, " new-session -d ") && !strings.Contains(p.Args, own) {
				clients = append(clients, fmt.Sprintf("%d %s", pid, p.Args))
			}
		}
		return len(clients) == 0, fmt.Sprintf("clients %v", clients)
	})
	typed = plainClaudeLive(c, typed, own)
	return s
}

// unwrapped is a screen with its line breaks taken out, so a line the terminal wrapped reads whole.
func unwrapped(screen string) string { return strings.ReplaceAll(screen, "\n", "") }
