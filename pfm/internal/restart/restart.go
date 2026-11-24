// Package restart owns `pfm restart`: it finds every live chat, every helper a
// chat carries, Herdr, every service pfm installs or relies on and every other
// pfm process on this host, stops them downstream first, then starts back up,
// upstream first, only what was running before, never a unit found stopped.
// A chat in a Herdr pane comes back with Herdr's own restore, its pane, split
// and conversation as they were; every other chat, and every chat under
// --no-restore, is ended and named. Every step is verified and reported on its
// own line; a failed step is named and the rest still run.
//
// Entry points: Main and Worker (launch.go) run it; BuildPlan (discover.go) finds what it stops;
// StaleHerdrIndicators and ConvergeHerdrIndicators (herdr_indicators.go) serve doctor and install.
// Files:
//   - discover.go: the plan, family by family: helpers, services, the MCP daemon, pfm.
//   - chats.go: the chats family: which chat comes back with Herdr's restore.
//   - chats_wait.go: the verification that each such chat is live again, within one shared deadline.
//   - herdr.go: the Herdr server family: its status, stop and start.
//   - herdr_tree.go: what one Herdr session's server runs, and the outlivers its stop proves gone.
//   - herdr_indicators.go: the stale Herdr indicator painters.
//   - launch.go: the front that launches the detached worker and follows its log.
//   - live.go: the host's real doors, the fleet scan and the chat verbs among them.
//   - processes.go: the process table and the stops of the processes found in it.
//   - services.go: the service managers (systemd, launchd) and their units.
package restart

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// The families, in stop order: downstream first. Start walks the same units
// backwards, so the upstream ones come back first.
const (
	FamilyChats     = "chats"
	FamilyHelpers   = "chat-helpers"
	FamilyHerdr     = "herdr"
	FamilyServices  = "services"
	FamilyMCPDaemon = "mcp-daemon"
	FamilyPFM       = "pfm"
)

// ExitMarker opens the worker's last log line; the front that launched the
// worker stops following the log when it reads it.
const ExitMarker = "restart: exit "

// ChatReport is one chat as the closing report names it; NotBack is why a
// restart that brings chats back ends this one.
type ChatReport struct {
	Engine    string
	Name      string
	SessionID string
	NotBack   string
}

// Unit is one process or service the restart stops and, unless Start is nil,
// starts again; one that StaysStopped it does neither. Stop and Start each verify their own outcome: the detail they
// return is what was seen, never what was hoped.
type Unit struct {
	Family   string
	Name     string
	FoundBy  string
	StopHow  string
	StartHow string
	Stop     func(context.Context) (string, error)
	Start    func(context.Context) (string, error)
	Chat     *ChatReport
	// Before is the unit's state when the plan was built, as its manager
	// answered it.
	Before string
	// StaysStopped marks a unit that was not running when the plan was
	// built: the restart neither stops nor starts it, and names it.
	StaysStopped bool
}

// staysStopped is the line that names a unit the restart leaves stopped, in
// the plan and in the result lines alike.
func (unit *Unit) staysStopped() string {
	return "stays stopped (was not running before: " + unit.Before + ")"
}

// restarts reports whether the start phase starts unit again.
func (unit *Unit) restarts() bool { return unit.Start != nil && !unit.StaysStopped }

// Finding is how one family was looked for: what the enumerator saw, or why
// it could not look. A failed look is never "nothing to stop".
type Finding struct {
	Family string
	Detail string
	Err    error
}

// Plan is the ordered restart: each family's finding and the units in stop
// order.
type Plan struct {
	Findings []Finding
	Units    []Unit
	// Last names the process that carries the restart to its end.
	Last string
	// Restore marks a plan that brings each chat in a Herdr pane back.
	Restore bool
}

// Print writes the whole ordered plan and changes nothing.
func (plan Plan) Print(w io.Writer) {
	chats := "chats are not brought back"
	if plan.Restore {
		chats = "a chat in a Herdr pane comes back with Herdr's restore, every other chat is ended"
	}
	fmt.Fprintln(
		w,
		"restart plan: stop downstream first, then start upstream first only what was running before; "+chats,
	)
	for _, finding := range plan.Findings {
		if finding.Err != nil {
			fmt.Fprintf(w, "find   %-14s FAILED  %v\n", finding.Family, finding.Err)
			continue
		}
		fmt.Fprintf(w, "find   %-14s ok      %s\n", finding.Family, finding.Detail)
	}
	step := 0
	for index := range plan.Units {
		unit := &plan.Units[index]
		if unit.StaysStopped {
			continue
		}
		step++
		fmt.Fprintf(w, "stop  %2d  %-14s %s\n", step, unit.Family, unit.Name)
		fmt.Fprintf(w, "          found: %s\n          stop:  %s\n", unit.FoundBy, unit.StopHow)
	}
	step = 0
	for index := len(plan.Units) - 1; index >= 0; index-- {
		unit := &plan.Units[index]
		if !unit.restarts() {
			continue
		}
		step++
		was := "restart (was up)"
		if unit.Before != "" {
			was = "restart (was up: " + unit.Before + ")"
		}
		fmt.Fprintf(w, "start %2d  %-14s %s  %s\n          start: %s\n",
			step, unit.Family, unit.Name, was, unit.StartHow)
	}
	if step == 0 {
		fmt.Fprintln(w, "start     nothing found to start again")
	}
	for index := range plan.Units {
		unit := &plan.Units[index]
		if unit.StaysStopped {
			fmt.Fprintf(w, "keep      %-14s %s  %s\n          found: %s\n",
				unit.Family, unit.Name, unit.staysStopped(), unit.FoundBy)
		}
	}
	fmt.Fprintf(w, "last      %-14s %s\n", FamilyPFM, plan.Last)
}

// Execute runs the plan: every stop in order, then every start in reverse,
// one result line each, one line per unit that stays stopped, untouched, then
// the chats it ended, brought back or lost, and the exit marker. It
// returns 1 when any finding or step failed, 0 otherwise.
func (plan Plan) Execute(ctx context.Context, w io.Writer) int {
	failed := 0
	for _, finding := range plan.Findings {
		if finding.Err != nil {
			failed++
			fmt.Fprintf(w, "find   %-14s FAILED  %v\n", finding.Family, finding.Err)
		}
	}
	var ended, back, lost []ChatReport
	for index := range plan.Units {
		unit := &plan.Units[index]
		if unit.StaysStopped {
			continue
		}
		detail, err := unit.Stop(ctx)
		if !plan.report(w, "stop ", unit, detail, err) {
			failed++
			continue
		}
		if unit.Chat != nil && !unit.restarts() {
			ended = append(ended, *unit.Chat)
		}
	}
	for index := len(plan.Units) - 1; index >= 0; index-- {
		unit := &plan.Units[index]
		if !unit.restarts() {
			continue
		}
		detail, err := unit.Start(ctx)
		started := plan.report(w, "start", unit, detail, err)
		if !started {
			failed++
		}
		switch {
		case unit.Chat != nil && started:
			back = append(back, *unit.Chat)
		case unit.Chat != nil:
			lost = append(lost, *unit.Chat)
		}
	}
	for index := range plan.Units {
		unit := &plan.Units[index]
		if unit.StaysStopped {
			fmt.Fprintf(w, "keep   %-14s %s  %s\n", unit.Family, unit.Name, unit.staysStopped())
		}
	}
	plan.reportChats(w, ended, back, lost)
	fmt.Fprintf(w, "last   %-14s %s\n", FamilyPFM, plan.Last)
	code := 0
	if failed > 0 {
		code = 1
		fmt.Fprintf(w, "restart: %d step(s) FAILED\n", failed)
	} else {
		fmt.Fprintln(w, "restart: every step verified")
	}
	fmt.Fprintf(w, "%s%d\n", ExitMarker, code)
	return code
}

// reportChats names every chat the restart ended (with why it does not come
// back), brought back, or promised and lost, with the command that reopens it.
func (plan Plan) reportChats(w io.Writer, ended, back, lost []ChatReport) {
	fmt.Fprintf(w, "restart: ended %d chat(s)\n", len(ended))
	for _, chat := range ended {
		line := fmt.Sprintf("  ended  %-8s %-24s %s", chat.Engine, chat.Name, chat.SessionID)
		if chat.NotBack != "" {
			line += "  not brought back: " + chat.NotBack
		}
		fmt.Fprintln(w, line)
	}
	if !plan.Restore {
		return
	}
	fmt.Fprintf(w, "restart: brought back %d chat(s)\n", len(back))
	for _, chat := range back {
		fmt.Fprintf(w, "  back   %-8s %-24s %s\n", chat.Engine, chat.Name, chat.SessionID)
	}
	if len(lost) == 0 {
		return
	}
	fmt.Fprintf(w, "restart: %d chat(s) did not come back; reopen each by hand\n", len(lost))
	for _, chat := range lost {
		fmt.Fprintf(
			w,
			"  lost   %-8s %-24s %s  pfm chat open %s\n",
			chat.Engine,
			chat.Name,
			chat.SessionID,
			chat.SessionID,
		)
	}
}

// report writes one step's result line and says whether it succeeded.
func (plan Plan) report(w io.Writer, phase string, unit *Unit, detail string, err error) bool {
	if err != nil {
		fmt.Fprintf(w, "%s  %-14s %s  FAILED  %s\n", phase, unit.Family, unit.Name, stepLine(err.Error()))
		return false
	}
	fmt.Fprintf(w, "%s  %-14s %s  ok  %s\n", phase, unit.Family, unit.Name, detail)
	return true
}

// stepLine keeps a multi-line error on its step's one line.
func stepLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
