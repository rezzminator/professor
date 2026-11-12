//go:build journey

package journey

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// hostRule: from inside the branch's Claude chat in Herdr, through its shell, the chat asks pfm for a
// new chat in tmux in words (`--backend tmux`): pfm starts it detached on its own tmux server and
// attaches no client in the pane. It branches itself: the fork is a live Claude chat in its own pane
// of the same Herdr session, its tab marked idle, its lineage recorded under its seat. It opens the
// live tmux-hosted peer: pfm refuses with exit 10, as a tmux client is never drawn in a Herdr pane.
// Then the owner, in a plain terminal outside Herdr, starts a chat while the branch's Herdr server
// runs (his shell names its session): it is born in that Herdr session, not in tmux. The fleet check
// then finds no tmux chat placed for a caller in Herdr unless asked for in words, and no tmux client
// in a Herdr pane, and the chats the step started end. The chat then branches itself a second time:
// the second fork is a chat of its own with its lineage, the parent answers its next turn, and the
// fork's end takes its lineage.
func hostRule(c *harness.Ctx, s *State) {
	sock := herdrSocket(*s)
	pane := liveSeats(c, sock)[s.Chat.Session][1]
	tmuxChat, fork, outside := c.Unique("tmuxnew"), c.Unique("fork"), c.Unique("outside")
	again := c.Unique("again")
	c.Cleanup(func() {
		for _, name := range []string{tmuxChat, fork, outside, again} {
			if r := c.Read("pfm", "chat", "status", name, "--json"); r.Code != 0 {
				continue
			}
			if r := c.Read("pfm", "chat", "end", name); r.Code != 0 {
				c.Logf("teardown: pfm chat end %s exited %d: %s%s", name, r.Code, r.Stderr, r.Stdout)
			}
		}
	})
	term := c.Terminal(s.Term)
	answers := play(c, term, s,
		cli("cd $HOME/work/demo && pfm chat new --engine cc --backend tmux --name "+tmuxChat),
		cli("pfm chat branch --name "+fork),
		cli("pfm chat open "+s.Mesh.PeerName),
	)
	for index := range answers {
		answers[index].Text = strings.TrimSpace(answers[index].Text)
	}
	tmuxSock := filepath.Base(
		seatsBackend(c, tmuxChat, "tmux"),
	) // the seat holds the socket's path; pfm prints its name
	if want := (answer{0, "cc\t" + tmuxChat + "\t" + tmuxSock + "\tnamed\tlisted\nattach: tmux -L " + tmuxSock +
		" attach -t " + tmuxSock}); answers[0] != want {
		c.Failf("pfm chat new --backend tmux from Herdr pane %s = %+v, want %+v", pane, answers[0], want)
	}
	branched(c, s, answers[1], fork, pane)
	if want := (answer{10, "pfm chat open: execute action: host refused at tmux attach: this terminal is Herdr pane " +
		pane + ", where a tmux client is never attached; open the chat from a plain terminal"}); answers[2] != want {
		c.Failf("pfm chat open %s from Herdr pane %s = %+v, want %+v", s.Mesh.PeerName, pane, answers[2], want)
	}
	owner := c.Terminal(meshOwner)
	out, errs, code := typedRunWithin(c, owner, "outside-new",
		"cd $HOME/work/demo && HERDR_SESSION="+s.Session+" pfm chat new --engine cc --name "+outside, stepBound)
	born := regexp.MustCompile(`^cc\t` + regexp.QuoteMeta(outside) + `\therdr\t` + regexp.QuoteMeta(sock) +
		`\tw[0-9A-Za-z]+:p[0-9A-Za-z]+\tstarting\n$`)
	if code != 0 || !born.MatchString(out) || errs != "" {
		c.Failf("pfm chat new from a plain terminal while Herdr session %s runs: exit %d, stdout %q, stderr %q; "+
			"want exit 0, stdout matching %s", s.Session, code, out, errs, born)
	}
	fleetConsistent(c, *s)
	// The steps after this one read the branch's fleet as it was before it: its chats end here.
	for _, name := range []string{tmuxChat, fork, outside} {
		if r := c.Read("pfm", "chat", "end", name); r.Code != 0 {
			c.Failf("pfm chat end %s exited %d: %s%s", name, r.Code, r.Stderr, r.Stdout)
		}
		if name == fork {
			vanished(c, name)
		} else {
			notLive(c, name)
		}
	}
	seat := branchAgain(c, s, term, again, pane) // once its first fork ended
	if r := c.Read("pfm", "chat", "end", again); r.Code != 0 {
		c.Failf("pfm chat end %s exited %d: %s%s", again, r.Code, r.Stderr, r.Stdout)
	}
	vanished(c, again)
	c.Poll("the ended fork "+again+"'s lineage under seat "+seat+" gone", stepBound, func() (bool, string) {
		lineage := forkLineage(c, seat)
		return lineage == "", fmt.Sprintf("lineage %q", lineage)
	})
}

// branchAgain is the owner forking one conversation more than once: the chat in pane branches itself as fork,
// a fork of its own, and the parent answers its next turn. It is the fork's seat.
func branchAgain(c *harness.Ctx, s *State, term *harness.Terminal, fork, pane string) string {
	second := play(c, term, s, cli("pfm chat branch --name "+fork))
	second[0].Text = strings.TrimSpace(second[0].Text)
	seat := branched(c, s, second[0], fork, pane)
	after := play(c, term, s, cli("echo parent-after-"+fork))
	after[0].Text = strings.TrimSpace(after[0].Text)
	if want := (answer{0, "parent-after-" + fork}); after[0] != want {
		c.Failf("the parent's turn after its branch %s answered %+v, want %+v", fork, after[0], want)
	}
	return seat
}

// branched checks the answer of `pfm chat branch --name fork` typed in the parent's Herdr pane: its one line, the
// fork a live Claude chat of its own session in its own pane of the same Herdr session, its tab idle, its lineage
// under its seat the parent's session. It is the fork's seat.
func branched(c *harness.Ctx, s *State, got answer, fork, pane string) string {
	sock := herdrSocket(*s)
	forkSession, forkSeat, forkPane := forkOf(c, fork, sock)
	if want := (answer{0, "Branched " + s.Chat.Session[:8] + "… as \"" + fork + "\" into Herdr pane " + forkPane +
		" (socket " + sock + ") — live in pfm ls; open it with pfm chat open \"" + fork + "\"."}); got != want {
		c.Failf("pfm chat branch from Herdr pane %s = %+v, want %+v", pane, got, want)
	}
	if forkSession == s.Chat.Session || forkPane == pane {
		c.Failf("the fork %s runs session %s in pane %s, want its own session and pane apart from its parent's "+
			"%s in %s", fork, forkSession, forkPane, s.Chat.Session, pane)
	}
	if lineage := forkLineage(c, forkSeat); lineage != s.Chat.Session {
		c.Failf("the fork %s's lineage under seat %s = %q, want its parent %s", fork, forkSeat, lineage, s.Chat.Session)
	}
	c.Poll(fork+" listed live and its tab idle", stepBound, func() (bool, string) {
		rows, _ := liveChats(c, sock)
		label := paneTabs(c, *s)[forkPane]
		return rows[forkSession] == [2]string{"live-claude", sock} && label == markedLabel("idle", fork),
			fmt.Sprintf("row %v, tab %q", rows[forkSession], label)
	})
	return forkSeat
}

// forkLineage is the parent session recorded under a fork's seat, "" when none is.
func forkLineage(c *harness.Ctx, seat string) string {
	return strings.TrimSpace(stateSQL(c,
		"select val from meta where key = 'branch-seat:"+seat+"'"))
}

// seatsBackend is the host socket of the live seat named name on backend, failing unless exactly one.
func seatsBackend(c *harness.Ctx, name, backend string) string {
	out := strings.TrimSpace(stateSQL(c,
		"select coalesce(tmux_socket, herdr_socket) from seat where state = 'live' and backend = '"+backend+
			"' and name = '"+strings.ReplaceAll(name, "'", "''")+"'"))
	if out == "" || strings.Contains(out, "\n") {
		c.Failf("live %s seats named %s: %q, want exactly one", backend, name, out)
	}
	return out
}

// forkOf is the live Herdr seat named name on sock: its session id, seat id and pane.
func forkOf(c *harness.Ctx, name, sock string) (session, seat, pane string) {
	seats := seatsWhere(c, "state = 'live' and backend = 'herdr' and name = '"+
		strings.ReplaceAll(name, "'", "''")+"'"+onSocket(sock))
	if len(seats) != 1 {
		c.Failf("live Herdr seats named %s on %s = %v, want one", name, sock, seats)
	}
	for session, row := range seats {
		return session, row[0], row[1]
	}
	return "", "", ""
}
