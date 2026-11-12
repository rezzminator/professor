//go:build journey

package journey

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// chatNew: in the first pane of the branch's Herdr session the owner leaves the picker for the pane's
// shell, sets pfm's Claude permission mode to auto in his config, and types `pfm chat new --engine cc
// --name {name}` in his project (Codex is logged in too, and pfm's default engine is then Codex's);
// pfm prints the new chat's line (engine, name, backend, socket, pane) and Herdr opens a tab named for
// it. Underneath: one live Claude chat of that name on the session's socket, its seat on the printed
// pane, its engine under the session's Herdr server and started with `--permission-mode auto` from the
// config, never the bypass flag (escape chat-birth/new-from-cli, permission mode). The owner then puts
// his config back as it was.
func chatNew(c *harness.Ctx, s State) State {
	term, owner := c.Terminal(s.Term), c.Terminal(meshOwner)
	config := strings.TrimPrefix(strings.TrimSpace(read(c, "pfm", "config", "validate")), "config valid: ")
	c.Cleanup(func() { restoreConfig(c, config) })
	// Written aside and moved in: a pfm another branch runs meanwhile reads the old config or the new one,
	// never a half-written file.
	if code := shellRun(owner, "auto", `cp -p `+config+` `+config+`.mesh && jq '.claude.permissionMode = "auto"' `+
		config+`.mesh >`+config+`.auto && mv `+config+`.auto `+config, stepBound); code != 0 {
		c.Failf("setting claude.permissionMode to auto in %s exited %d:\n%s", config, code, owner.Screen())
	}
	term.Press(harness.Esc)
	term.WaitFor(prompt)
	s.Name, s.Project = c.Unique("mesh"), s.Home+"/work/demo"
	sock := herdrSocket(s)
	term.Type("cd $HOME/work/demo && pfm chat new --engine cc --name " + s.Name + "\r")
	var pane string
	term.WaitScreen("pfm chat new's line for "+s.Name, 20*time.Second, func(screen string) bool {
		for _, line := range strings.Split(screen, "\n") {
			if f := strings.Fields(
				paneText(line),
			); len(f) == 6 && f[0] == "cc" && f[1] == s.Name && f[2] == "herdr" &&
				f[3] == sock {
				pane = f[4]
				return true
			}
		}
		return false
	})
	term.WaitScreen("a Herdr tab named "+s.Name, stepBound, func(screen string) bool {
		top, _, _ := strings.Cut(screen, "\n")
		return slices.Contains(strings.Fields(top), s.Name)
	})
	c.Poll("one live Claude chat named "+s.Name+" with a live seat on "+pane, 20*time.Second, func() (bool, string) {
		rows := namedChats(c, sock)
		seats := liveSeats(c, sock)
		seen := fmt.Sprintf("chats %v, seats %v", rows, seats)
		if len(rows) != 1 || len(seats) != 1 {
			return false, seen
		}
		for session, row := range rows {
			if seat, ok := seats[session]; !ok || row != [2]string{"live-claude", s.Name} || seat[1] != pane {
				return false, seen
			}
			s.Chat = Chat{Session: session, Seat: seats[session][0]}
		}
		return true, seen
	})
	s.Chat.Engine = engineOf(c, s)
	argv := strings.Fields(processes(c)[s.Chat.Engine].Args)
	at := slices.Index(argv, "--permission-mode")
	if at < 0 || at+1 >= len(argv) || argv[at+1] != "auto" || slices.Contains(argv, "--dangerously-skip-permissions") {
		c.Failf("engine %d of %s started without `--permission-mode auto` from the config: %s",
			s.Chat.Engine, s.Name, strings.Join(argv, " "))
	}
	if code := shellRun(owner, "restore", "mv "+config+".mesh "+config, stepBound); code != 0 {
		c.Failf("restoring %s exited %d:\n%s", config, code, owner.Screen())
	}
	fleetConsistent(c, s)
	return s
}

// restoreConfig puts the owner's config back from the copy chatNew kept, if it is still there.
func restoreConfig(c *harness.Ctx, config string) {
	if r := c.Read("test", "-e", config+".mesh"); r.Code != 0 {
		return
	}
	if r := c.Read("mv", config+".mesh", config); r.Code != 0 {
		c.Logf("teardown: restore %s exited %d: %s", config, r.Code, r.Stderr)
	}
}

// namedChats is `pfm ls --tsv`'s live rows on sock: session id -> engine kind and pfm name.
func namedChats(c *harness.Ctx, sock string) map[string][2]string {
	rows := map[string][2]string{}
	for _, line := range strings.Split(read(c, "pfm", "ls", "--tsv"), "\n") {
		if f := strings.Split(line, "\t"); len(f) >= 11 && strings.HasPrefix(f[0], "live-") && f[10] == sock {
			rows[f[1]] = [2]string{f[0], f[4]}
		}
	}
	return rows
}

// paneText is a line of the Herdr screen right of its sidebar: the pane's own text.
func paneText(line string) string {
	if _, pane, found := strings.Cut(line, "│"); found {
		return pane
	}
	return line
}
