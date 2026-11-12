//go:build journey

package journey

import (
	"fmt"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// scheduleHidden: the branch's chat has ended. The owner kills it, so it is hidden, then arms a one-time
// schedule on it, and the scheduler's tick (`pfm internal schedule-fire`, the command pfm's timer runs)
// fires it once it is due: the chat comes back live, its kill is lifted, and its transcript holds the
// scheduled prompt once, answered.
func scheduleHidden(c *harness.Ctx, s State) State {
	c.Cleanup(func() { endScheduled(c, s.Chat.Session) })
	if r := c.Read("pfm", "chat", "kill", s.Chat.Session); r.Code != 0 {
		c.Failf("pfm chat kill %s exited %d: %s%s", s.Chat.Session, r.Code, r.Stderr, r.Stdout)
	}
	if got := hiddenRows(c, s.Chat.Session); got != "1" {
		c.Failf("killed rows for %s after pfm chat kill = %s, want 1", s.Chat.Session, got)
	}
	scheduleDelivered(c, s)
	if got := hiddenRows(c, s.Chat.Session); got != "0" {
		c.Failf("killed rows for %s after the schedule woke it = %s, want 0", s.Chat.Session, got)
	}
	return s
}

// scheduleDelivered arms a one-time schedule on the branch's chat, makes it due and runs the scheduler's
// tick until the schedule has fired; then the chat
// is live on one seat, its transcript holds the scheduled message exactly once, and pfm's history of it
// answers that message with the scheduled turn's reply.
func scheduleDelivered(c *harness.Ctx, s State) {
	reply := c.Unique("scheduled-reply")
	prompt := `a scheduled turn mock-engine: {"type":"turn","reply":"` + reply + `"}`
	set := c.Read("pfm", "chat", "schedule", "set", "--in", "1m", "--prompt", prompt, s.Chat.Session)
	id := strings.TrimSpace(set.Stdout)
	if set.Code != 0 || id == "" || strings.ContainsAny(id, " \n") {
		c.Failf("pfm chat schedule set on %s: exit %d, stdout %q, stderr %q; want exit 0 printing its id",
			s.Chat.Session, set.Code, set.Stdout, set.Stderr)
	}
	// The shortest delay is a minute: the step makes the schedule due now rather than wait it out.
	read(c, "sqlite3", stateDB(), fmt.Sprintf("update schedules set next_fire_ns = %d where id = %s",
		time.Now().UnixNano(), id))
	var tick string
	c.Poll("schedule "+id+" fired by pfm internal schedule-fire", 90*time.Second, func() (bool, string) {
		fire := c.Read("pfm", "internal", "schedule-fire")
		tick = fmt.Sprintf("exit %d: %s%s", fire.Code, fire.Stdout, fire.Stderr)
		fired := strings.TrimSpace(c.Read("sqlite3", stateDB(),
			"select count(*) from schedules where id = "+id+" and last_fired_ns <> 0").Stdout)
		return fired == "1", fmt.Sprintf(
			"fired %s; the tick exited %d: %s%s",
			fired,
			fire.Code,
			fire.Stdout,
			fire.Stderr,
		)
	})
	head := "⏰ pfm schedule " + id + " (once): " + prompt
	c.Poll("chat "+s.Chat.Session+" live, the scheduled turn answered", 30*time.Second, func() (bool, string) {
		seats := seatsWhere(c, "state = 'live' and session_id = '"+s.Chat.Session+"'")
		var delivered []string
		for _, text := range userTexts(c, transcriptOf(c, s)) {
			if strings.HasPrefix(text, head) {
				delivered = append(delivered, text)
			}
		}
		history := c.Read("sh", "-c", `cd "$1" && pfm chat history "$2"`, "sh", s.Project, s.Chat.Session)
		turns := historyTurn.FindAllStringSubmatch(history.Stdout, -1)
		answer := "(no scheduled turn)"
		for i, turn := range turns {
			if turn[1] == "user" && turn[2] == head && i+1 < len(turns) {
				answer = turns[i+1][1] + ": " + turns[i+1][2]
			}
		}
		pane := "(no live seat)"
		for _, seat := range seats {
			pane = c.Read("tmux", "-L", seat[0], "capture-pane", "-p").Stdout
		}
		return len(seats) == 1 && len(delivered) == 1 && answer == "assistant: "+reply,
			fmt.Sprintf("live seats %v, scheduled messages %q, history's turn after it %q (history exit %d: %s);"+
				" the firing tick %s; the seat's pane:\n%s", seats, delivered, answer, history.Code, history.Stderr, tick, pane)
	})
}

// hiddenRows counts the killed rows of session.
func hiddenRows(c *harness.Ctx, session string) string {
	return strings.TrimSpace(read(c, "sqlite3", stateDB(), "select count(*) from hidden where uuid = '"+session+"'"))
}

// liveEngine is the pid of session's one engine, 0 when none or several run.
func liveEngine(c *harness.Ctx, session string) int {
	var found []int
	for pid, id := range engines(processes(c)) {
		if id == session {
			found = append(found, pid)
		}
	}
	if len(found) != 1 {
		return 0
	}
	return found[0]
}

// endScheduled ends the chat a schedule woke, and waits for no engine of it to be left.
func endScheduled(c *harness.Ctx, session string) {
	if rows, _ := liveChats(c, ""); rows[session] != [2]string{} {
		if r := c.Read("pfm", "chat", "end", session); r.Code != 0 {
			c.Logf("teardown: pfm chat end %s exited %d: %s", session, r.Code, r.Stderr)
		}
	}
	chatEnded(c, session, 20*time.Second)
}
