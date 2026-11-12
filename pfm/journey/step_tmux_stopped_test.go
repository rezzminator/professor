//go:build journey

package journey

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// tmuxStopped: beside the restart chain's two live Herdr chats the owner starts a tmux-hosted chat
// (`pfm chat new --name {held}`) and stops its tmux server (kill -STOP). The picker still draws within
// its bound, both healthy chats counted live, and once left prints its warning about the stopped
// server naming kill -CONT; claude
// typed in a plain terminal exits 1 within 10 s naming the server that did not answer. After kill -CONT
// no tmux client is left hanging and claude typed again launches a live chat.
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
	c.Expect(`"msg":"tmux.exec","cmd":"picker"`)
	c.Expect(`"hook":"claude-launch","decision":"error","exit":1`)
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
	typed := c.Terminal("tmux-stopped-claude")
	typed.WaitFor(prompt)
	typed.Type("cd $HOME/work/demo && claude\r")
	want = "pfm internal launch: place chat: caller unresolved: look up the caller: read tmux panes on " + socket +
		": tmux server did not answer within 2s: " + socket + "pfm: harness exited 1 — press any key to close this terminal"
	typed.WaitScreen("claude's exit naming "+socket, 10*time.Second, func(screen string) bool {
		return strings.Contains(unwrapped(screen), want)
	})
	typed.Press(harness.Enter)
	if code := shellRun(term, "cont-"+held, fmt.Sprintf("clear && kill -CONT %d", server), 10*time.Second); code != 0 {
		c.Failf("kill -CONT %d exited %d", server, code)
	}
	c.Poll("no tmux client left hanging", stepBound, func() (bool, string) {
		var clients []string
		for pid, p := range processes(c) {
			if f := strings.Fields(p.Args); len(f) > 1 && filepath.Base(f[0]) == "tmux" &&
				!strings.Contains(p.Args, " new-session -d ") {
				clients = append(clients, fmt.Sprintf("%d %s", pid, p.Args))
			}
		}
		return len(clients) == 0, fmt.Sprintf("clients %v", clients)
	})
	again := State{Home: s.Home, Term: "tmux-continued-claude", Project: filepath.Join(s.Home, "work", "demo")}
	launched := c.Terminal(again.Term)
	launched.WaitFor(prompt)
	c.Cleanup(func() { stopPlainChat(c, &again, launched) })
	launched.Type("cd $HOME/work/demo && claude\r")
	launched.WaitFor("❯", 20*time.Second)
	again = plainClaudeLive(c, again, attached(c, launched))
	return s
}

// unwrapped is a screen with its line breaks taken out, so a line the terminal wrapped reads whole.
func unwrapped(screen string) string { return strings.ReplaceAll(screen, "\n", "") }
