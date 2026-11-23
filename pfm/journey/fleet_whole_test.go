//go:build journey

package journey

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/journey/harness"
)

// cpuTable is the artifact every look at pfm's CPU time appends to (cpu.tsv under JOURNEY_ARTIFACTS, its
// header written by TestMain): per pfm process, the tree's elapsed time at the look and the CPU seconds
// the process used. infra/fence/journey/budget.sh judges it: a process over its share of the tree's time
// is spinning (an indicator, a blinker or a daemon). "" when JOURNEY_ARTIFACTS is unset: nothing is kept.
var cpuTable string

// cpuTableHeader is cpu.tsv's first line.
const cpuTableHeader = "elapsed_s\tpid\tcpu_s\tcommand\n"

// treeElapsed reads the time since TestJourney started the tree; the CPU table records it, never judges it.
var treeElapsed func() time.Duration

// fleetWhole fails the step unless the whole machine is consistent, every branch's chats at once: the
// join's check, run only when no branch is mid-change (the steps after `day`). Every live chat has one
// row and one live seat; every live seat on a running Herdr session sits on a pane it has; no branch's
// own Herdr session still runs; every
// engine process belongs to a live chat; no pfm process leaves a zombie child; every pfm process's CPU
// time joins cpu.tsv for budget.sh (recordCPU); every settled picker is idle (idleFaults).
func fleetWhole(c *harness.Ctx) {
	rows, listed := liveChats(c, "")
	seats := liveSeats(c, "")
	faults := chatFaults(rows, listed, seats)
	sessions := herdrSessions(c)
	for i := range sessions {
		sock := herdrSocket(sessions[i])
		faults = append(faults, paneFaults(sock, liveSeats(c, sock), herdrPanes(c, sessions[i]))...)
		if name := sessions[i].Session; name != "" { // a branch's own session: its teardown stops it
			faults = append(faults, fmt.Sprintf("Herdr session %s still runs after its branch ended", name))
		}
	}
	table := processes(c)
	faults = append(faults, engineFaults(table, rows, seats, func(process) bool { return true })...)
	faults = append(faults, zombies(c, table)...)
	recordCPU(c, table)
	faults = append(faults, idleFaults(c, table)...)
	if len(faults) > 0 { // every live seat's whole row, so a fault names its socket, pane and owner
		c.Artifact("seats.txt", []byte(stateSQL(c, "-header",
			"select * from seat where state = 'live'")))
	}
	failFleet(c, "the machine's fleet", faults)
}

// herdrSessions is every running Herdr session, as `herdr session list` names them.
func herdrSessions(c *harness.Ctx) []State {
	var list struct {
		Sessions []struct {
			Name    string `json:"name"`
			Default bool   `json:"default"`
			Running bool   `json:"running"`
		} `json:"sessions"`
	}
	out := read(c, "herdr", "session", "list", "--json")
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		c.Failf("herdr session list --json: %v:\n%s", err, out)
	}
	var running []State
	for _, s := range list.Sessions {
		if !s.Running {
			continue
		}
		session := State{Home: ownerHome, Session: s.Name}
		if s.Default {
			session.Session = ""
		}
		running = append(running, session)
	}
	return running
}

// pfmProcess reports whether p runs pfm's own binary.
func pfmProcess(p process) bool {
	argv0, _, _ := strings.Cut(p.Args, " ")
	return filepath.Base(argv0) == "pfm"
}

// zombies is a fault for every defunct child of a pfm process: pfm started it and never reaped it.
func zombies(c *harness.Ctx, table map[int]process) []string {
	proc := procTable(c)
	identify, ok := proc.(gather.ProcIdentity)
	if !ok {
		c.Failf("the process table reader %T names no process state", proc)
	}
	pids, err := proc.PIDs()
	if err != nil {
		c.Failf("list the process table: %v", err)
	}
	var faults []string
	for _, pid := range pids {
		identity, err := identify.ProcessIdentity(pid)
		if err != nil || !identity.Zombie {
			continue // gone since the listing, or alive
		}
		stat, err := proc.Stat(pid)
		if err != nil {
			continue
		}
		if parent, ok := table[stat.ParentPID]; ok && pfmProcess(parent) {
			faults = append(faults, fmt.Sprintf("zombie pid %d (%s) under pfm pid %d: %s",
				pid, identity.Command, parent.Pid, parent.Args))
		}
	}
	return faults
}

// recordCPU appends to cpuTable one row per pfm process of table: the tree's elapsed time now, its pid,
// the CPU seconds it used and its command line. Counters it cannot read fail the step.
func recordCPU(c *harness.Ctx, table map[int]process) {
	counters, _, err := gather.ProcessCounters("") // an unreadable record is left out: it has exited
	if err != nil {
		c.Failf("read the processes' CPU time: %v", err)
	}
	elapsed := treeElapsed()
	var rows strings.Builder
	for pid, counter := range counters {
		p, ok := table[pid]
		if !ok || !pfmProcess(p) || counter.CPUUnknown {
			continue
		}
		used := time.Duration(counter.CPUTicks) * 10 * time.Millisecond
		fmt.Fprintf(
			&rows,
			"%.3f\t%d\t%.3f\t%s\n",
			elapsed.Seconds(),
			pid,
			used.Seconds(),
			strings.Join(strings.Fields(p.Args), " "),
		)
	}
	if cpuTable == "" {
		return
	}
	file, err := os.OpenFile(cpuTable, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		c.Failf("open the CPU table %s: %v", cpuTable, err)
	}
	_, err = file.WriteString(rows.String())
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		c.Failf("append %d bytes to the CPU table %s: %v", rows.Len(), cpuTable, err)
	}
}

// pickerShare is the most CPU time an open picker may use of its own life: one left open in an unwatched
// pane refreshes on demand, never on a fixed fast cadence.
const pickerShare = 0.10

// pickerSettled is how long a picker must have lived before its share is judged: its first paint costs.
const pickerSettled = 30 * time.Second

// idleFaults is a fault for every pfm picker (`pfm` or `pfm ls`) settled past pickerSettled whose CPU
// time exceeds pickerShare of its own elapsed time.
func idleFaults(c *harness.Ctx, table map[int]process) []string {
	counters, _, err := gather.ProcessCounters("")
	if err != nil {
		c.Failf("read the processes' CPU time: %v", err)
	}
	birth, ok := procTable(c).(gather.ProcBirth)
	if !ok {
		c.Failf("the process table reader names no process birth")
	}
	now := time.Now()
	var faults []string
	pickers := 0
	for pid, p := range table {
		if !pfmProcess(p) {
			continue
		}
		_, args, _ := strings.Cut(p.Args, " ")
		counter, counted := counters[pid]
		if args != "" && args != "ls" || !counted || counter.CPUUnknown {
			continue
		}
		born, err := birth.Birth(pid)
		if err != nil {
			continue // gone since the listing
		}
		lived := now.Sub(time.Unix(born, 0))
		used := time.Duration(counter.CPUTicks) * 10 * time.Millisecond
		if lived >= pickerSettled {
			pickers++
		}
		if lived >= pickerSettled && used.Seconds() > pickerShare*lived.Seconds() {
			faults = append(faults, fmt.Sprintf("picker pid %d used %.1fs of CPU in its %.1fs (over %.0f%%): %s",
				pid, used.Seconds(), lived.Seconds(), pickerShare*100, p.Args))
		}
	}
	c.Logf("idle check read %d settled picker(s)", pickers)
	return faults
}
