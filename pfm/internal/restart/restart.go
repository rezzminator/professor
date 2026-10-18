// Package restart owns `pfm restart`: it finds every live chat, every helper a
// chat carries, Herdr, every service pfm installs or relies on and every other
// pfm process on this host, stops them downstream first, then starts everything
// except the chats back up upstream first. Every step is verified and reported
// on its own line; a failed step is named and the rest still run.
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
	FamilySearch    = "search-backend"
	FamilyPFM       = "pfm"
)

// ExitMarker opens the worker's last log line; the front that launched the
// worker stops following the log when it reads it.
const ExitMarker = "restart: exit "

// EndedChat is one chat the restart ended, as its closing report names it.
type EndedChat struct {
	Engine    string
	Name      string
	SessionID string
}

// Unit is one process or service the restart stops and, unless Start is nil,
// starts again. Stop and Start each verify their own outcome: the detail they
// return is what was seen, never what was hoped.
type Unit struct {
	Family   string
	Name     string
	FoundBy  string
	StopHow  string
	StartHow string
	Stop     func(context.Context) (string, error)
	Start    func(context.Context) (string, error)
	Chat     *EndedChat
}

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
}

// Print writes the whole ordered plan and changes nothing.
func (plan Plan) Print(w io.Writer) {
	fmt.Fprintln(w, "restart plan: stop downstream first, then start upstream first; chats are not brought back")
	for _, finding := range plan.Findings {
		if finding.Err != nil {
			fmt.Fprintf(w, "find   %-14s FAILED  %v\n", finding.Family, finding.Err)
			continue
		}
		fmt.Fprintf(w, "find   %-14s ok      %s\n", finding.Family, finding.Detail)
	}
	for index, unit := range plan.Units {
		fmt.Fprintf(w, "stop  %2d  %-14s %s\n", index+1, unit.Family, unit.Name)
		fmt.Fprintf(w, "          found: %s\n          stop:  %s\n", unit.FoundBy, unit.StopHow)
	}
	step := 0
	for index := len(plan.Units) - 1; index >= 0; index-- {
		unit := plan.Units[index]
		if unit.Start == nil {
			continue
		}
		step++
		fmt.Fprintf(w, "start %2d  %-14s %s\n          start: %s\n", step, unit.Family, unit.Name, unit.StartHow)
	}
	if step == 0 {
		fmt.Fprintln(w, "start     nothing found to start again")
	}
	fmt.Fprintf(w, "last      %-14s %s\n", FamilyPFM, plan.Last)
}

// Execute runs the plan: every stop in order, then every start in reverse,
// one result line each, then the chats it ended and the exit marker. It
// returns 1 when any finding or step failed, 0 otherwise.
func (plan Plan) Execute(ctx context.Context, w io.Writer) int {
	failed := 0
	for _, finding := range plan.Findings {
		if finding.Err != nil {
			failed++
			fmt.Fprintf(w, "find   %-14s FAILED  %v\n", finding.Family, finding.Err)
		}
	}
	var ended []EndedChat
	for _, unit := range plan.Units {
		detail, err := unit.Stop(ctx)
		if !plan.report(w, "stop ", unit, detail, err) {
			failed++
			continue
		}
		if unit.Chat != nil {
			ended = append(ended, *unit.Chat)
		}
	}
	for index := len(plan.Units) - 1; index >= 0; index-- {
		unit := plan.Units[index]
		if unit.Start == nil {
			continue
		}
		detail, err := unit.Start(ctx)
		if !plan.report(w, "start", unit, detail, err) {
			failed++
		}
	}
	fmt.Fprintf(w, "restart: ended %d chat(s)\n", len(ended))
	for _, chat := range ended {
		fmt.Fprintf(w, "  ended  %-8s %-24s %s\n", chat.Engine, chat.Name, chat.SessionID)
	}
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

// report writes one step's result line and says whether it succeeded.
func (plan Plan) report(w io.Writer, phase string, unit Unit, detail string, err error) bool {
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
