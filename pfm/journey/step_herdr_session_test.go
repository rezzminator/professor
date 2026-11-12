//go:build journey

package journey

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// herdrSession: the owner starts a Herdr session of the branch's own in a plain terminal (`herdr
// --session {name}`, its own server and socket), and sees pfm's picker in its first pane; Herdr lists
// that one pane. When the branch's subtree ends, failed or not, its chats are ended and the session
// stopped and deleted, and no engine of it survives, so nothing of it reaches the joins.
func herdrSession(c *harness.Ctx, s State) State {
	s.Session = c.Unique("herdr")
	s.Term, s.Project = s.Session, s.Home
	term := c.Terminal(s.Term)
	term.WaitFor(prompt)
	c.Cleanup(func() { stopHerdrSession(c, s) })
	term.Type("herdr --session " + s.Session + "\r")
	return firstPane(c, s)
}

// phase runs one part of a teardown; a failure in it is already reported, and the next part still runs.
func phase(c *harness.Ctx, part func()) {
	defer func() {
		if r := recover(); r != nil {
			c.Logf("teardown: %v", r)
		}
	}()
	part()
}

// stopHerdrSession tears the branch's Herdr session down with the owner's own commands: `pfm chat end`
// for every live chat on its socket, a wait for no live seat on it, then `herdr session stop` and `herdr
// session delete`, then a wait for no engine of the branch (under its Herdr server, or of a seat ever on
// its socket) to survive. A command that fails is logged: the joins judge what is left.
func stopHerdrSession(c *harness.Ctx, s State) {
	sock := herdrSocket(s)
	var record strings.Builder
	defer func() { c.Artifact("teardown.txt", []byte(record.String())) }()
	run := func(argv ...string) {
		r := c.Read(argv...)
		fmt.Fprintf(&record, "%q exit %d\n%s%s", argv, r.Code, r.Stdout, r.Stderr)
		if r.Code != 0 {
			c.Logf("teardown of Herdr session %s: %q exited %d: %s%s", s.Session, argv, r.Code, r.Stderr, r.Stdout)
		}
	}
	if !slices.ContainsFunc(herdrSessions(c), func(r State) bool { return r.Session == s.Session }) {
		run("herdr", "session", "delete", s.Session)
		return // stopped already, or never started: nothing on it to end
	}
	endSeated(c, sock, run)
	ever := seatsWhere(c, "session_id is not null"+onSocket(sock))
	branch := map[int]bool{}
	table := processes(c)
	server := herdrServer(table, s)
	for pid, p := range table {
		if _, seated := ever[p.Session]; p.Engine != "" && (seated || server != 0 && under(table, pid, server)) {
			branch[pid] = true
		}
	}
	run("herdr", "session", "stop", s.Session)
	run("herdr", "session", "delete", s.Session)
	phase(c, func() {
		c.Poll("no engine of Herdr session "+s.Session+" left", 20*time.Second, func() (bool, string) {
			var left []string
			for pid, p := range processes(c) {
				if _, seated := ever[p.Session]; p.Engine != "" && (branch[pid] || seated) {
					left = append(left, fmt.Sprintf("%d %s", pid, p.Args))
				}
			}
			return len(left) == 0, fmt.Sprintf("%v", left)
		})
	})
}

// endSeated ends every chat with a live seat on sock with `pfm chat end`, then waits for no live seat
// on it: `pfm chat end` only schedules the end.
func endSeated(c *harness.Ctx, sock string, run func(argv ...string)) {
	for session := range liveSeats(c, sock) {
		run("pfm", "chat", "end", session)
	}
	phase(c, func() {
		c.Poll("no live seat on "+sock, 20*time.Second, func() (bool, string) {
			seats := liveSeats(c, sock)
			return len(seats) == 0, fmt.Sprintf("%v", seats)
		})
	})
}
