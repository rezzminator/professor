//go:build journey

package journey

import (
	"fmt"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// openElsewhere: in the pane the unkill left, a workspace Herdr opened in the home folder, the owner leaves
// the picker for the shell and types `pfm chat open {session}`; the chat comes back live in a pane of a
// workspace labelled for its project, demo, its cwd the project, and no workspace is labelled for home.
func openElsewhere(c *harness.Ctx, s State) State {
	term := c.Terminal(s.Term)
	term.Press(harness.Esc)
	term.WaitFor(prompt, stepBound)
	if label := workspaceLabel(c, s); label != "~" {
		c.Failf("Herdr workspace before the open = %q, want ~ (opened in home)", label)
	}
	term.Type("pfm chat open " + s.Chat.Session + "\r")
	sock := herdrSocket(s)
	c.Poll("chat "+s.Chat.Session+" live again on "+sock, 20*time.Second, func() (bool, string) {
		seats := liveSeats(c, sock)
		seat, ok := seats[s.Chat.Session]
		if !ok || len(seats) != 1 {
			return false, fmt.Sprintf("live seats %v", seats)
		}
		s.Chat.Seat, s.Pane = seat[0], seat[1]
		return true, ""
	})
	s.Chat.Engine = engineOf(c, s)
	c.Poll("the one Herdr workspace labelled demo", stepBound, func() (bool, string) {
		label := workspaceLabel(c, s)
		return label == "demo", "workspace " + label
	})
	if row := tsvRow(c, s.Chat.Session); len(row) < 4 || row[3] != s.Project {
		c.Failf("pfm ls --tsv row of the opened chat = %q, want its cwd %s", row, s.Project)
	}
	return s
}

// killCLI: from the second terminal the owner types `pfm chat kill {name}`; it says it closed the chat's
// pane, Herdr lists the pane no more, no engine of the chat is left, it is killed in state and `pfm chat
// ls --all` lists it no more. Then, in the home pane's shell, `claude --resume {session}` in the project
// brings the chat back, and the resume lifts the kill: pfm lists it once, live, and no longer killed.
func killCLI(c *harness.Ctx, s State) State {
	cli := c.Terminal(s.Term + "-cli")
	out, errs, code := typedRun(c, cli, "kill", "pfm chat kill "+s.Name)
	if want := "killed " + s.Chat.Session + "\tclosing pane " + s.Pane + " on socket " + s.Chat.Seat + "\n"; code != 0 ||
		out != want ||
		errs != "" {
		c.Failf("pfm chat kill %s: exit %d, stdout %q, stderr %q; want exit 0, stdout %q, no stderr",
			s.Name, code, out, errs, want)
	}
	c.Poll("pane "+s.Pane+" and engine "+fmt.Sprint(s.Chat.Engine)+" gone", stepBound, func() (bool, string) {
		_, pane := herdrPanes(c, s)[s.Pane]
		_, engine := processes(c)[s.Chat.Engine]
		return !pane && !engine, fmt.Sprintf("pane listed %t, engine alive %t", pane, engine)
	})
	c.Poll(s.Name+" killed in state and gone from pfm chat ls --all", stepBound, func() (bool, string) {
		listed := read(c, "pfm", "chat", "ls", "--all")
		hidden := strings.TrimSpace(
			stateSQL(c, "select count(*) from hidden where uuid = '"+s.Chat.Session+"'"),
		)
		return hidden == "1" && chatLsRow(listed, s.Name) == nil,
			"killed rows " + hidden + ", listed:\n" + listed
	})
	// Herdr gave the emptied workspace a new pane on pfm's picker: the owner leaves it for the shell.
	term := c.Terminal(s.Term)
	term.Press(harness.Esc)
	term.WaitFor(prompt, stepBound)
	term.Type("cd " + s.Project + " && claude --resume " + s.Chat.Session + "\r")
	term.WaitFor("❯", 20*time.Second)
	c.Poll("the resumed chat live and no longer killed", stepBound, func() (bool, string) {
		hidden := strings.TrimSpace(
			stateSQL(c, "select count(*) from hidden where uuid = '"+s.Chat.Session+"'"),
		)
		rows := listRows(c, s.Chat.Session)
		return hidden == "0" && len(rows) == 1 && strings.HasPrefix(rows[0], "live-claude\t"),
			fmt.Sprintf("killed rows %s, listed %q", hidden, rows)
	})
	return s
}
