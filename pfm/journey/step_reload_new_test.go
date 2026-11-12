//go:build journey

package journey

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// reloadNew: from a second terminal the owner names the resumed chat, then reboots it into a fresh conversation (`pfm chat
// reload --new`, a follow-up prompt with it); the pane comes back on a new session that answers the follow-up
// and carries the chat's name, the conversation it left is listed under the name with " (before --new)", and
// `pfm chat status {name}` answers for the new session: every by-name verb reaches the live chat.
func reloadNew(c *harness.Ctx, s State) State {
	cli := c.Terminal(s.Term + "-cli")
	cli.WaitFor(prompt)
	before := s.Chat
	s.Name = c.Unique("renewed")
	nameFromCLI(c, cli, s, before.Session)
	name := s.Name
	reply := c.Unique("new-pong")
	command := "pfm chat reload --new --sock " + herdrSocket(s) + " --pane " + s.Pane +
		" --then '" + directive(turnStep(reply)) + "'"
	if code := shellRun(cli, "reload-new", command, 30*time.Second); code != 0 {
		c.Failf("%s exited %d, want 0", command, code)
	}
	c.Terminal(s.Term).WaitFor("⏺ "+reply, 20*time.Second)
	sock := herdrSocket(s)
	c.Poll("one live seat on "+s.Pane+" under a session after "+before.Session, 20*time.Second, func() (bool, string) {
		seats := liveSeats(c, sock)
		for session, seat := range seats {
			if session != before.Session && seat[1] == s.Pane && len(seats) == 1 {
				s.Chat.Session, s.Chat.Seat = session, seat[0]
				return true, ""
			}
		}
		return false, fmt.Sprintf("live seats %v", seats)
	})
	c.Poll("the new session named "+name+", the one it left named for --new", stepBound, func() (bool, string) {
		got := [2]string{rowName(c, s.Chat.Session), rowName(c, before.Session)}
		return got == [2]string{name, name + " (before --new)"}, fmt.Sprintf("names (new, left) = %q", got)
	})
	out, errs, code := typedRun(c, cli, "status", "pfm chat status '"+name+"' --json")
	var status struct {
		Name      string `json:"name"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(out), &status); code != 0 || err != nil ||
		[2]string{status.Name, status.SessionID} != [2]string{name, s.Chat.Session} {
		c.Failf("pfm chat status %q --json: exit %d (%v), stdout %q, stderr %q; want name %q and session %s",
			name, code, err, out, errs, name, s.Chat.Session)
	}
	if row := tsvRow(c, s.Chat.Session); len(row) < 1 || row[0] != "live-claude" {
		c.Failf("pfm ls --tsv row of the new session = %q, want it live", row)
	}
	s.Chat.Engine = engineOf(c, s)
	fleetConsistent(c, s)
	return s
}
