//go:build journey

package journey

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// backgroundJob: a schedule is armed on the branch's chat, then a turn sends the chat to a Claude Code
// background job: the conversation goes on under a new session in a `claude bg-spare` daemon, the old transcript
// ends on a continued-in record naming it, and the pane's engine keeps the session it showed. The daemon's
// process record names the new session.
func backgroundJob(c *harness.Ctx, s State) State {
	set := c.Read("pfm", "chat", "schedule", "set", "--in", "1h", "--prompt", "a later turn", s.Chat.Session)
	s.Schedule = strings.TrimSpace(set.Stdout)
	if set.Code != 0 || s.Schedule == "" || strings.ContainsAny(s.Schedule, " \n") {
		c.Failf("pfm chat schedule set on %s: exit %d, stdout %q, stderr %q; want exit 0 printing its id",
			s.Chat.Session, set.Code, set.Stdout, set.Stderr)
	}
	reply := c.Unique("job-reply")
	term := c.Terminal(s.Term)
	term.Type(`send it to the background mock-engine: [{"type":"background_job"},{"type":"turn","reply":"` +
		reply + `"}]` + "\r")
	term.WaitFor("⏺ "+reply, 20*time.Second)
	s.Left = s.Chat.Session
	continued := read(c, "sh", "-c", `cat "$HOME"/.claude/projects/*/"$1".jsonl "$HOME"/.cc/*/projects/*/"$1".jsonl `+
		`2>/dev/null | grep -m1 '"continued-in"'`, "sh", s.Left)
	var record struct {
		ContinuedIn string `json:"continuedInSessionId"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(continued)), &record); err != nil || record.ContinuedIn == "" {
		c.Failf("continued-in record of %s = %q (%v), want one naming its successor", s.Left, continued, err)
	}
	s.Chat.Session = record.ContinuedIn
	c.Poll("a claude bg-spare process recording "+s.Chat.Session, stepBound, func() (bool, string) {
		pid, line, records := jobRecord(c, s.Chat.Session)
		if line == "" {
			return false, "records:\n" + records
		}
		// The daemon serves the job away from the pane: not the pane engine's child.
		p, ok := processes(c)[pid]
		args := strings.Fields(p.Args)
		daemon := ok && len(args) == 2 && args[1] == "bg-spare" && p.Ppid != s.Chat.Engine
		return daemon, fmt.Sprintf("record %s, process %q (parent %d, the pane engine %d)",
			line, p.Args, p.Ppid, s.Chat.Engine)
	})
	return s
}

// jobOneChat (escape herdr/background-job-two-chats): the continued chat stays one live chat on its pane — the
// native seat keeps its session as its identity — and its new session is listed nowhere else: not as a second
// chat, not as the daemon's detached agent, and never offered for resume while the job runs it.
func jobOneChat(c *harness.Ctx, s State) State {
	c.Poll("one live row for the seat's "+s.Left+" and none for "+s.Chat.Session, stepBound, func() (bool, string) {
		seat, successor := listRows(c, s.Left), listRows(c, s.Chat.Session)
		live := len(seat) == 1 && strings.HasPrefix(seat[0], "live-claude\t") &&
			strings.HasSuffix(seat[0], herdrSocket(s))
		return live && len(successor) == 0, fmt.Sprintf("seat rows %q, successor rows %q", seat, successor)
	})
	return s
}

// jobScheduleFollows (escape herdr/schedule-in-dead-segment): the schedule armed before the job moved with the
// chat onto its new session.
func jobScheduleFollows(c *harness.Ctx, s State) State {
	c.Poll("schedule "+s.Schedule+" on "+s.Chat.Session, stepBound, func() (bool, string) {
		read(c, "pfm", "ls", "--tsv")
		got := strings.TrimSpace(stateSQL(c, "select session_id from schedules where id = "+s.Schedule))
		return got == s.Chat.Session, "session_id " + got
	})
	if r := c.Read("pfm", "chat", "schedule", "rm", s.Schedule); r.Code != 0 {
		c.Failf("pfm chat schedule rm %s exited %d: %s%s", s.Schedule, r.Code, r.Stdout, r.Stderr)
	}
	return s
}

// jobOpenFindsSeat (escape herdr/open-continued-starts-second): `pfm chat open {new session}` finds the seat
// the chat runs on and attaches to its pane; no second engine starts on the session.
func jobOpenFindsSeat(c *harness.Ctx, s State) State {
	before := chatEngines(c, s)
	cli := c.Terminal(s.Term + "-cli")
	cli.WaitFor(prompt)
	cli.Type("pfm chat open " + s.Chat.Session + "\r")
	want, attach := "herdr agent attach "+s.Pane, 0
	c.Poll("pfm chat open "+s.Chat.Session+" running `"+want+"`", stepBound, func() (bool, string) {
		table := processes(c)
		for _, p := range table {
			if strings.HasSuffix(p.Args, want) &&
				strings.HasSuffix(table[p.Ppid].Args, "pfm chat open "+s.Chat.Session) {
				attach = p.Pid
				return true, ""
			}
		}
		return false, fmt.Sprintf("no %q beneath the open", want)
	})
	if after := chatEngines(c, s); !maps.Equal(after, before) {
		c.Failf("the chat's claude processes after pfm chat open %s:\n%s\nwant the ones before it:\n%s",
			s.Chat.Session, engineList(after), engineList(before))
	}
	// The attach is the owner's view of the pane; the step ends it rather than type into the chat.
	if r := c.Read("kill", strconv.Itoa(attach)); r.Code != 0 {
		c.Failf("end the attach %d: exit %d: %s", attach, r.Code, r.Stderr)
	}
	cli.WaitFor(prompt, stepBound)
	return s
}

// jobRecord reads the owner's Claude session records for the background job serving session: the job's pid
// and its record line, "" when no record names it yet, and every record read, for a failure to print.
func jobRecord(c *harness.Ctx, session string) (pid int, line, records string) {
	records = read(c, "sh", "-c", `cat "$HOME"/.claude/sessions/*.json "$HOME"/.cc/*/sessions/*.json 2>/dev/null; true`)
	for _, l := range strings.Split(records, "\n") {
		var job struct {
			PID       int    `json:"pid"`
			SessionID string `json:"sessionId"`
			Kind      string `json:"kind"`
		}
		if json.Unmarshal([]byte(l), &job) == nil && job.SessionID == session && job.Kind == "bg" {
			return job.PID, l, records
		}
	}
	return 0, "", records
}

// chatEngines is every Claude process of the branch's chat, by pid: under the branch's Herdr server, naming
// the session it runs or the one it left, or the background-job daemon serving it. Other branches' engines
// are not read: they start and end while this step runs (a herdr-mesh chat ends at its own turn's end, after
// its step has passed).
func chatEngines(c *harness.Ctx, s State) map[int]process {
	daemon, _, _ := jobRecord(c, s.Chat.Session)
	table := processes(c)
	server := herdrServer(table, s)
	found := map[int]process{}
	for pid, p := range table {
		if p.Engine != "claude" {
			continue
		}
		if pid == daemon || p.Session == s.Chat.Session || p.Session == s.Left ||
			server != 0 && under(table, pid, server) {
			found[pid] = p
		}
	}
	return found
}

// engineList prints engines one line each, by pid: the pid, its first two argv words and the session it names.
func engineList(engines map[int]process) string {
	pids := slices.Sorted(maps.Keys(engines))
	lines := make([]string, 0, len(pids))
	for _, pid := range pids {
		words := strings.Fields(engines[pid].Args)
		lines = append(lines, fmt.Sprintf("%d %s (session %q)",
			pid, strings.Join(words[:min(2, len(words))], " "), engines[pid].Session))
	}
	return strings.Join(lines, "\n")
}
