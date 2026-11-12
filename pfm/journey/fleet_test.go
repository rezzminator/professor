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

func stateDB() string { return filepath.Join(ownerHome, ".local", "state", "pfm", "pfm.db") }

// herdrSocket is the socket of the branch's Herdr session: the default session's for the bare `herdr`.
func herdrSocket(s State) string {
	if s.Session == "" {
		return filepath.Join(s.Home, ".config", "herdr", "herdr.sock")
	}
	return filepath.Join(s.Home, ".config", "herdr", "sessions", s.Session, "herdr.sock")
}

// herdrCLI is the argv of a herdr command aimed at the branch's Herdr session.
func herdrCLI(s State, args ...string) []string {
	if s.Session == "" {
		return append([]string{"herdr"}, args...)
	}
	return append([]string{"herdr", "--session", s.Session}, args...)
}

// The readers below take a scope: the host socket a branch's chats live on (herdrSocket(s) for a
// Herdr branch), or "" for the whole machine, which only the joins read (fleetWhole).

// liveChats is `pfm ls --tsv`'s live rows on sock: session id -> engine kind and host socket, plus
// every session id as many times as it was listed.
func liveChats(c *harness.Ctx, sock string) (rows map[string][2]string, listed []string) {
	rows = map[string][2]string{}
	out := read(c, "pfm", "ls", "--tsv")
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "kind\tid\t") {
		c.Failf("pfm ls --tsv printed no header:\n%s", out)
	}
	for _, line := range lines[1:] {
		f := strings.Split(line, "\t")
		if len(f) < 11 || !strings.HasPrefix(f[0], "live-") || sock != "" && f[10] != sock {
			continue
		}
		rows[f[1]] = [2]string{f[0], f[10]}
		listed = append(listed, f[1])
	}
	return rows, listed
}

// liveSeats is the state database's live seats on sock: session id -> seat id, Herdr pane.
func liveSeats(c *harness.Ctx, sock string) map[string][2]string {
	return seatsWhere(c, "state = 'live'"+onSocket(sock))
}

// onSocket is the seat query's filter for sock, empty for the whole machine.
func onSocket(sock string) string {
	if sock == "" {
		return ""
	}
	return " and (herdr_socket = '" + strings.ReplaceAll(sock, "'", "''") + "' or tmux_socket = '" +
		strings.ReplaceAll(sock, "'", "''") + "')"
}

// seatsWhere is the seats matching a SQL condition: session id -> seat id, Herdr pane.
func seatsWhere(c *harness.Ctx, condition string) map[string][2]string {
	seats := map[string][2]string{}
	// pfm writes the database while a branch reads it: wait out its lock, as pfm's own readers do.
	out := read(c, "sqlite3", "-cmd", ".timeout 5000", "-separator", "\t", stateDB(),
		"select session_id, seat_id, coalesce(herdr_pane, '') from seat where "+condition)
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if f := strings.Split(line, "\t"); len(f) == 3 {
			seats[f[0]] = [2]string{f[1], f[2]}
		}
	}
	return seats
}

// herdrPanes is the pane list of the branch's Herdr session: pane id -> the agent Herdr sees in it.
func herdrPanes(c *harness.Ctx, s State) map[string]string {
	var list struct {
		Result struct {
			Panes []struct {
				PaneID string `json:"pane_id"`
				Agent  string `json:"agent"`
			} `json:"panes"`
		} `json:"result"`
	}
	out := read(c, herdrCLI(s, "pane", "list")...)
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		c.Failf("herdr pane list: %v:\n%s", err, out)
	}
	panes := map[string]string{}
	for _, p := range list.Result.Panes {
		panes[p.PaneID] = p.Agent
	}
	return panes
}

// process is one row of the process table.
type process struct {
	Pid, Ppid int
	Args      string
	Engine    string // claude, codex or opencode for an engine's process; empty for any other
	Session   string // an engine's session id, when its argv or its open rollout names it
}

// processes reads the process table through pfm's own reader (/proc on Linux, sysctl on macOS); a
// process that exits between the listing and its read is left out, and so is a zombie, whose argv is
// gone (zombies reads those).
func processes(c *harness.Ctx) map[int]process {
	proc := procTable(c)
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
		p := process{Pid: pid, Ppid: stat.ParentPID, Args: strings.Join(argv, " ")}
		p.Engine, p.Session = engineIdentity(proc, pid, argv)
		table[pid] = p
	}
	return table
}

func procTable(c *harness.Ctx) gather.ProcFS {
	proc, err := gather.OpenProcFS("")
	if err != nil {
		c.Failf("open the process table: %v", err)
	}
	return proc
}

var (
	claudeSession   = regexp.MustCompile(`--(?:session-id|resume) ([0-9a-f-]{36})`)
	codexSession    = regexp.MustCompile(` resume ([0-9a-f-]{36})`)
	openCodeSession = regexp.MustCompile(` (?:--session|-s)[ =](\S+)`)
)

// engineIdentity names an engine process by pfm's own spelling of each engine's argv, and the session
// it runs: Claude's and OpenCode's from their flags, Codex's from its resume argument or the rollout
// it holds open. A fresh OpenCode names its session nowhere a process shows: it reads "".
func engineIdentity(proc gather.ProcFS, pid int, argv []string) (engine, session string) {
	args := strings.Join(argv, " ")
	pick := func(re *regexp.Regexp) string {
		if m := re.FindStringSubmatch(args); m != nil {
			return m[1]
		}
		return ""
	}
	switch {
	case gather.IsClaudeCommand(argv):
		return "claude", pick(claudeSession)
	case gather.IsCodexCommand(argv):
		if session = pick(codexSession); session != "" {
			return "codex", session
		}
		links, _ := proc.FDLinks(pid) // unreadable: the session stays unknown, judged as such
		for _, link := range links {
			if id := gather.CodexRolloutID(link.Target); id != "" {
				return "codex", id
			}
		}
		return "codex", ""
	case gather.IsOpenCodeCommand(argv):
		return "opencode", pick(openCodeSession)
	}
	return "", ""
}

// engines is every engine process in table: pid -> its session id.
func engines(table map[int]process) map[int]string {
	found := map[int]string{}
	for pid, p := range table {
		if p.Engine != "" {
			found[pid] = p.Session
		}
	}
	return found
}

// underHerdr reports whether pid runs under a Herdr process, the owner of every Herdr pane.
func underHerdr(table map[int]process, pid int) bool {
	for seen := 0; pid > 1 && seen < 64; seen++ {
		pid = table[pid].Ppid
		if f := strings.Fields(table[pid].Args); len(f) > 0 && filepath.Base(f[0]) == "herdr" {
			return true
		}
	}
	return false
}

// herdrServer is the pid of the branch's Herdr server in table, 0 when none runs: the `herdr server`
// process the owner's own `herdr` command for the session started, which every pane of it runs under.
func herdrServer(table map[int]process, s State) int {
	client := strings.Join(herdrCLI(s), " ")
	for pid, p := range table {
		f := strings.Fields(p.Args)
		if len(f) == 2 && filepath.Base(f[0]) == "herdr" && f[1] == "server" && table[p.Ppid].Args == client {
			return pid
		}
	}
	return 0
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

// fleetConsistent fails the step unless the branch's own Herdr session is consistent: every live chat
// on its socket has exactly one row, one live seat and a pane the session still has; every engine of
// the branch (under its Herdr server, or of a seat ever on its socket) belongs to a live chat on it.
// Other branches' chats are not read: they change while this one is judged.
func fleetConsistent(c *harness.Ctx, s State) {
	sock := herdrSocket(s)
	rows, listed := liveChats(c, sock)
	seats := liveSeats(c, sock)
	panes := herdrPanes(c, s)
	ever := seatsWhere(c, "session_id is not null"+onSocket(sock))
	table := processes(c)
	faults := chatFaults(rows, listed, seats)
	server := herdrServer(table, s)
	faults = append(faults, paneFaults(sock, seats, panes)...)
	faults = append(faults, engineFaults(table, rows, seats, func(p process) bool {
		_, seated := ever[p.Session]
		return seated || server != 0 && under(table, p.Pid, server)
	})...)
	failFleet(c, "the branch's fleet ("+sock+")", faults)
}

// chatFaults: every live row listed once and on a live seat, every live seat on a live row.
func chatFaults(rows map[string][2]string, listed []string, seats map[string][2]string) []string {
	var faults []string
	times := map[string]int{}
	for _, session := range listed {
		times[session]++
	}
	for session, n := range times {
		if n != 1 {
			faults = append(faults, fmt.Sprintf("chat %s listed %d times", session, n))
		}
		if _, ok := seats[session]; !ok {
			faults = append(faults, fmt.Sprintf("chat %s (%s) has no live seat", session, rows[session][0]))
		}
	}
	for session := range seats {
		if _, ok := rows[session]; !ok {
			faults = append(faults, fmt.Sprintf("live seat of %s has no chat row", session))
		}
	}
	return faults
}

// paneFaults: every live seat on the Herdr socket sock on a pane it still has.
func paneFaults(sock string, seats map[string][2]string, panes map[string]string) []string {
	var faults []string
	for session, seat := range seats {
		if _, ok := panes[seat[1]]; !ok {
			faults = append(faults, fmt.Sprintf("chat %s's pane %s is not in %s's pane list", session, seat[1], sock))
		}
	}
	return faults
}

// engineFaults: every engine process judged (in scope) belongs to a live chat: its session is a live
// row, or, when it names none (a fresh OpenCode), it runs under Herdr while a live chat of its engine
// has a live seat.
func engineFaults(table map[int]process, rows, seats map[string][2]string, inScope func(process) bool) []string {
	seatedKind := map[string]bool{}
	for session := range seats {
		seatedKind[rows[session][0]] = true
	}
	var faults []string
	for _, p := range table {
		if p.Engine == "" || !inScope(p) {
			continue
		}
		if _, ok := rows[p.Session]; ok && p.Session != "" {
			continue
		}
		if p.Session == "" && seatedKind["live-"+p.Engine] && underHerdr(table, p.Pid) {
			continue
		}
		faults = append(faults, fmt.Sprintf("%s engine pid %d (session %q) belongs to no live chat: %s",
			p.Engine, p.Pid, p.Session, p.Args))
	}
	return faults
}

// failFleet fails the step with every fault, sorted, under what was judged.
func failFleet(c *harness.Ctx, what string, faults []string) {
	slices.Sort(faults)
	if len(faults) > 0 {
		c.Failf("%s is inconsistent:\n%s", what, strings.Join(faults, "\n"))
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
// slice, the chat list, Herdr's panes and the engine processes.
func evidence(c *harness.Ctx, records []string) {
	for _, term := range c.Terminals() {
		c.Logf("terminal %s at the failure:\n%s", term.Name(), term.Screen())
		c.Artifact("terminal-"+term.Name()+".txt", []byte(term.Screen()))
		c.Artifact("terminal-"+term.Name()+".raw", term.Raw())
	}
	c.Artifact("activity.jsonl", []byte(strings.Join(records, "\n")))
	reads := map[string][]string{
		"chats.txt":     {"pfm", "chat", "ls", "--all"},
		"sessions.json": {"herdr", "session", "list", "--json"},
		"panes.json":    {"herdr", "pane", "list"},
	}
	for name, argv := range reads {
		r := c.Read(argv...)
		c.Artifact(name, []byte(fmt.Sprintf("exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)))
	}
	var table strings.Builder
	for _, p := range processes(c) {
		fmt.Fprintf(&table, "%d %d %s\n", p.Pid, p.Ppid, p.Args)
	}
	c.Artifact("processes.txt", []byte(table.String()))
}
