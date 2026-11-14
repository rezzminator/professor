//go:build journey

package journey

import (
	"fmt"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// herdrStops: from a second terminal the owner stops the restart chain's Herdr server; Herdr leaves
// the first terminal at its prompt, every pane process of the session dies with both engines, no Herdr
// indicator daemon outlives its server, `pfm chat ls --all` answers and lists both chats dead, and no
// pfm process spins or is left a zombie. Each chat's account (its engine's CLAUDE_CONFIG_DIR) is read
// before the stop, for herdrReturns to hold the resumed engine to.
func herdrStops(c *harness.Ctx, s State) State {
	for i, chat := range s.Chats {
		env, err := procTable(c).Environ(chat.Engine)
		if err != nil || env["CLAUDE_CONFIG_DIR"] == "" {
			c.Failf("read engine %d of %s's CLAUDE_CONFIG_DIR before the Herdr stop: %q, %v", chat.Engine, chat.Name,
				env["CLAUDE_CONFIG_DIR"], err)
		}
		s.Chats[i].Account = env["CLAUDE_CONFIG_DIR"]
	}
	ctl := c.Terminal("restart-ctl")
	ctl.WaitFor(prompt)
	command := strings.Join(herdrCLI(s, "server", "stop"), " ")
	if code := shellRun(ctl, "herdr-stop", command, 20*time.Second); code != 0 {
		c.Failf("%s exited %d:\n%s", command, code, ctl.Screen())
	}
	c.Terminal(s.Term).WaitScreen("the prompt back where Herdr ran", 20*time.Second, func(screen string) bool {
		lines := strings.Split(strings.TrimRight(screen, "\n "), "\n")
		return strings.TrimSpace(lines[len(lines)-1]) == prompt
	})
	c.Poll(
		"no engine of the restart chain's chats, no orphan Herdr indicator daemon",
		stepBound,
		func() (bool, string) {
			table := processes(c)
			var left []string
			for pid, session := range engines(table) {
				for _, chat := range s.Chats {
					if session == chat.Session {
						left = append(left, fmt.Sprintf("engine %d (%s)", pid, session))
					}
				}
			}
			for pid, p := range table {
				if strings.HasSuffix(p.Args, "pfm internal herdr-indicators") && !underHerdr(table, pid) {
					left = append(left, fmt.Sprintf("herdr-indicators %d under ppid %d", pid, p.Ppid))
				}
			}
			return len(left) == 0, fmt.Sprintf("left %v", left)
		},
	)
	if code := shellRun(ctl, "chat-ls-after-stop", "clear && pfm chat ls --all", 20*time.Second); code != 0 {
		c.Failf("pfm chat ls --all after the Herdr stop exited %d:\n%s", code, ctl.Screen())
	}
	screen := ctl.Screen()
	for _, chat := range s.Chats {
		if got := chatLsState(screen, chat.Name); got != "dead" {
			c.Failf(
				"pfm chat ls --all state of %s after its Herdr server stopped = %q, want dead:\n%s",
				chat.Name,
				got,
				screen,
			)
		}
	}
	table := processes(c)
	if bad := append(spinning(c, table), zombies(c, table)...); len(bad) != 0 {
		c.Failf("pfm processes after the Herdr stop, spinning or zombie: %v", bad)
	}
	return s
}

// chatLsState is the state column of name's one row in a `pfm chat ls` screen (name · session · state
// …); "" when no row or more than one carries the name.
func chatLsState(screen, name string) string {
	state, rows := "", 0
	for _, line := range strings.Split(screen, "\n") {
		if f := strings.Fields(line); len(f) > 2 && f[0] == name {
			state, rows = f[2], rows+1
		}
	}
	if rows != 1 {
		return ""
	}
	return state
}
