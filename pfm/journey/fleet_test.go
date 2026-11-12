//go:build journey

package journey

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/journey/harness"
)

// prompt is the owner's zsh prompt, as infra/fence/journey/prepare.sh writes it.
const prompt = "owner %"

// shellRun types command into term followed by an echo of its exit code under label, waits for that
// line and returns the code: the owner reads the result off the screen.
func shellRun(term *harness.Terminal, label, command string, within time.Duration) int {
	term.Type(command + `; echo "` + label + ` exit=$?"` + "\r")
	marker := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(label) + ` exit=(\d+)$`)
	var code int
	term.WaitScreen(label+"'s exit line", within, func(screen string) bool {
		m := marker.FindStringSubmatch(screen)
		if m != nil {
			code, _ = strconv.Atoi(m[1])
		}
		return m != nil
	})
	return code
}

// read runs argv as the owner and returns its stdout; a non-zero exit fails the step.
func read(c *harness.Ctx, argv ...string) string {
	r := c.Read(argv...)
	if r.Code != 0 {
		c.Failf("%q exited %d: %s%s", argv, r.Code, r.Stderr, r.Stdout)
	}
	return r.Stdout
}

func stateDB(s State) string     { return filepath.Join(s.Home, ".local", "state", "pfm", "pfm.db") }
func herdrSocket(s State) string { return filepath.Join(s.Home, ".config", "herdr", "herdr.sock") }

// liveChats is `pfm ls --tsv`'s live rows: session id -> engine kind and host socket, plus every
// session id as many times as it was listed.
func liveChats(c *harness.Ctx) (rows map[string][2]string, listed []string) {
	rows = map[string][2]string{}
	out := read(c, "pfm", "ls", "--tsv")
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "kind\tid\t") {
		c.Failf("pfm ls --tsv printed no header:\n%s", out)
	}
	for _, line := range lines[1:] {
		f := strings.Split(line, "\t")
		if len(f) < 11 || !strings.HasPrefix(f[0], "live-") {
			continue
		}
		rows[f[1]] = [2]string{f[0], f[10]}
		listed = append(listed, f[1])
	}
	return rows, listed
}

// liveSeats is the state database's live seats: session id -> seat id, Herdr pane.
func liveSeats(c *harness.Ctx, s State) map[string][2]string {
	seats := map[string][2]string{}
	out := read(c, "sqlite3", "-separator", "\t", stateDB(s),
		"select session_id, seat_id, herdr_pane from seat where state = 'live'")
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if f := strings.Split(line, "\t"); len(f) == 3 {
			seats[f[0]] = [2]string{f[1], f[2]}
		}
	}
	return seats
}

// herdrPanes is Herdr's own pane list: pane id -> the agent Herdr sees in it.
func herdrPanes(c *harness.Ctx) map[string]string {
	var list struct {
		Result struct {
			Panes []struct {
				PaneID string `json:"pane_id"`
				Agent  string `json:"agent"`
			} `json:"panes"`
		} `json:"result"`
	}
	out := read(c, "herdr", "pane", "list")
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		c.Failf("herdr pane list: %v:\n%s", err, out)
	}
	panes := map[string]string{}
	for _, p := range list.Result.Panes {
		panes[p.PaneID] = p.Agent
	}
	return panes
}

// holder is one live herdr-shield holder.
type holder struct {
	ID   string
	Pane string
	Pid  int
}

// liveHolders is herdr-shield's list of live holders on this Herdr socket.
func liveHolders(c *harness.Ctx, s State) []holder {
	var list struct {
		Holders []struct {
			ID      string `json:"id"`
			Live    bool   `json:"holder_live"`
			Binding struct {
				Pane   string `json:"pane"`
				Socket string `json:"socket"`
			} `json:"binding"`
			Holder struct {
				Pid int `json:"pid"`
			} `json:"holder"`
		} `json:"holders"`
	}
	out := read(c, "env", "HERDR_SOCKET_PATH="+herdrSocket(s), "herdr-shield", "list", "--json")
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		c.Failf("herdr-shield list --json: %v:\n%s", err, out)
	}
	var live []holder
	for _, h := range list.Holders {
		if h.Live && h.Binding.Socket == herdrSocket(s) {
			live = append(live, holder{ID: h.ID, Pane: h.Binding.Pane, Pid: h.Holder.Pid})
		}
	}
	return live
}

// process is one row of the process table.
type process struct {
	Pid, Ppid int
	Args      string
}

// processes reads the process table through pfm's own reader (/proc on Linux, sysctl on macOS); a
// process that exits between the listing and its read is left out.
func processes(c *harness.Ctx) map[int]process {
	proc, err := gather.OpenProcFS("")
	if err != nil {
		c.Failf("open the process table: %v", err)
	}
	pids, err := proc.PIDs()
	if err != nil {
		c.Failf("list the process table: %v", err)
	}
	table := map[int]process{}
	for _, pid := range pids {
		argv, err := proc.Cmdline(pid)
		if err != nil || len(argv) == 0 {
			continue
		}
		stat, err := proc.Stat(pid)
		if err != nil {
			continue
		}
		table[pid] = process{Pid: pid, Ppid: stat.ParentPID, Args: strings.Join(argv, " ")}
	}
	return table
}

// engines is every fake Claude process: pid -> its session id (from --session-id or --resume).
func engines(table map[int]process) map[int]string {
	sessionFlag := regexp.MustCompile(`--(?:session-id|resume) ([0-9a-f-]{36})`)
	found := map[int]string{}
	for pid, p := range table {
		if !strings.HasPrefix(p.Args, "claude ") {
			continue
		}
		session := ""
		if m := sessionFlag.FindStringSubmatch(p.Args); m != nil {
			session = m[1]
		}
		found[pid] = session
	}
	return found
}

// under reports whether pid descends from ancestor in table.
func under(table map[int]process, pid, ancestor int) bool {
	for seen := 0; pid > 1 && seen < 64; seen++ {
		if pid == ancestor {
			return true
		}
		pid = table[pid].Ppid
	}
	return false
}

// fleetConsistent fails the step unless every live chat has exactly one row, one live seat and a pane
// Herdr still has; every engine process belongs to a live chat; every live holder holds a pane Herdr
// still has.
func fleetConsistent(c *harness.Ctx, s State) {
	rows, listed := liveChats(c)
	seats := liveSeats(c, s)
	panes := herdrPanes(c)
	var faults []string
	for _, session := range listed {
		if n := strings.Count(" "+strings.Join(listed, " ")+" ", " "+session+" "); n != 1 {
			faults = append(faults, fmt.Sprintf("chat %s listed %d times", session, n))
		}
		seat, ok := seats[session]
		if !ok {
			faults = append(faults, fmt.Sprintf("chat %s (%s) has no live seat", session, rows[session][0]))
			continue
		}
		if _, ok := panes[seat[1]]; !ok {
			faults = append(faults, fmt.Sprintf("chat %s's pane %s is not in Herdr's pane list", session, seat[1]))
		}
	}
	for session := range seats {
		if _, ok := rows[session]; !ok {
			faults = append(faults, fmt.Sprintf("live seat of %s has no chat row", session))
		}
	}
	for pid, session := range engines(processes(c)) {
		if _, ok := rows[session]; !ok {
			faults = append(faults, fmt.Sprintf("engine pid %d (session %q) belongs to no live chat", pid, session))
		}
	}
	for _, h := range liveHolders(c, s) {
		if _, ok := panes[h.Pane]; !ok {
			faults = append(faults, fmt.Sprintf("holder %s holds pane %s, which Herdr no longer has", h.ID, h.Pane))
		}
	}
	slices.Sort(faults)
	if len(faults) > 0 {
		c.Failf("the fleet is inconsistent:\n%s", strings.Join(faults, "\n"))
	}
}

// ownerHome is the owner's HOME, for the log gate and the evidence, which see no State.
var ownerHome string

// expectations holds what concurrent steps declared, by path, for their together node's window gate.
var expectations sync.Map

// logGate fails a step for an ERROR record in pfm's activity log written while it ran that it did not
// declare with Expect. A step beside other branches is judged once, with them, over the together
// node's whole window; a failed step always keeps its own log slice and the fleet's state as evidence.
func logGate(c *harness.Ctx, failed bool) {
	from, to := c.Window()
	records := activity(c, from, to)
	if failed {
		evidence(c, records)
	}
	if c.Concurrent() {
		expectations.Store(c.Path(), c.Expected())
		return
	}
	expected := c.Expected()
	expectations.Range(func(path, patterns any) bool {
		if strings.HasPrefix(path.(string), c.Path()+"/") {
			expected = append(expected, patterns.([]string)...)
		}
		return true
	})
	var undeclared []string
	for _, record := range records {
		if !strings.Contains(record, `"level":"ERROR"`) {
			continue
		}
		if !slices.ContainsFunc(expected, func(p string) bool { return strings.Contains(record, p) }) {
			undeclared = append(undeclared, record)
		}
	}
	if len(undeclared) > 0 {
		evidence(c, records)
		c.Failf(
			"%d undeclared ERROR records in pfm's activity log:\n%s",
			len(undeclared),
			strings.Join(undeclared, "\n"),
		)
	}
}

// activity is every activity-log record written within [from, to], as written.
func activity(c *harness.Ctx, from, to time.Time) []string {
	r := c.Read("cat", filepath.Join(ownerHome, ".local", "state", "pfm", "log", "pfm.jsonl"))
	if r.Code != 0 {
		return nil // no log yet: nothing of pfm has run
	}
	var records []string
	for _, line := range strings.Split(r.Stdout, "\n") {
		var head struct {
			TS time.Time `json:"ts"`
		}
		if json.Unmarshal([]byte(line), &head) != nil {
			continue
		}
		if !head.TS.Before(from) && (to.IsZero() || !head.TS.After(to)) {
			records = append(records, line)
		}
	}
	return records
}

// evidence saves what a failed step's owner would look at: every terminal its path opened, its log
// slice, the chat list, Herdr's panes, herdr-shield's holders and the engine processes.
func evidence(c *harness.Ctx, records []string) {
	for _, term := range c.Terminals() {
		c.Logf("terminal %s at the failure:\n%s", term.Name(), term.Screen())
		c.Artifact("terminal-"+term.Name()+".txt", []byte(term.Screen()))
		c.Artifact("terminal-"+term.Name()+".raw", term.Raw())
	}
	c.Artifact("activity.jsonl", []byte(strings.Join(records, "\n")))
	for name, argv := range map[string][]string{
		"chats.txt":   {"pfm", "chat", "ls", "--all"},
		"panes.json":  {"herdr", "pane", "list"},
		"holders.txt": {"env", "HERDR_SOCKET_PATH=" + herdrSocket(State{Home: ownerHome}), "herdr-shield", "list", "--json"},
	} {
		r := c.Read(argv...)
		c.Artifact(name, []byte(fmt.Sprintf("exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)))
	}
	var table strings.Builder
	for _, p := range processes(c) {
		fmt.Fprintf(&table, "%d %d %s\n", p.Pid, p.Ppid, p.Args)
	}
	c.Artifact("processes.txt", []byte(table.String()))
}
