//go:build journey

package journey

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/journey/harness"
)

// cpuShare is the most CPU time a pfm process may have used, as a share of the tree's elapsed time:
// past it, an indicator, a blinker or a daemon is spinning.
const cpuShare = 0.20

// treeStarted is when TestJourney started the tree; the CPU judgement measures against it.
var treeStarted time.Time

// fleetWhole fails the step unless the whole machine is consistent, every branch's chats at once: the
// join's check, run only when no branch is mid-change (the steps after `day`). Every live chat has one
// row and one live seat; every live seat on a running Herdr session sits on a pane it has; no branch's
// own Herdr session still runs; every
// engine process belongs to a live chat; no pfm process leaves a zombie child; no pfm process has burnt
// more than cpuShare of the tree's time.
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
	faults = append(faults, spinning(c, table)...)
	if len(faults) > 0 { // every live seat's whole row, so a fault names its socket, pane and owner
		c.Artifact("seats.txt", []byte(read(c, "sqlite3", "-cmd", ".timeout 5000", "-header", stateDB(),
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

// spinning is a fault for every pfm process whose CPU time exceeds cpuShare of the tree's elapsed time.
func spinning(c *harness.Ctx, table map[int]process) []string {
	counters, _, err := gather.ProcessCounters("") // an unreadable record is left out: it has exited
	if err != nil {
		c.Failf("read the processes' CPU time: %v", err)
	}
	elapsed := clock.Real.Now().Sub(treeStarted)
	var faults []string
	for pid, counter := range counters {
		p, ok := table[pid]
		if !ok || !pfmProcess(p) || counter.CPUUnknown {
			continue
		}
		used := time.Duration(counter.CPUTicks) * 10 * time.Millisecond
		if used.Seconds() > cpuShare*elapsed.Seconds() {
			faults = append(faults, fmt.Sprintf("pfm pid %d used %.1fs of CPU in the tree's %.1fs (over %.0f%%): %s",
				pid, used.Seconds(), elapsed.Seconds(), cpuShare*100, p.Args))
		}
	}
	return faults
}
