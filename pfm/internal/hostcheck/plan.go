package hostcheck

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// phase is a fix's place in the upgrade order. The rule: a fix runs only
// after every fix whose result it reads.
//
//   - config: the pfm config names the paths, the accounts and the MCP port
//     every later check and fix reads, so it moves first.
//   - database: the state and cache databases move with pfm's services
//     stopped, before any later step restarts them.
//   - store: the Claude store and each account dir must be real and hold
//     their own entries before the settings and MCP entries inside them are
//     edited.
//   - wiring: hook, plugin, MCP, settings env and shell wiring inside those
//     files.
//   - cleanup: leftovers nothing else reads.
//   - unknown: a class fixOrder does not name yet, after every known fix a
//     person applies, so a newly registered detector still prints, in a
//     fixed place.
//   - install: fixes that are a run of pfm install itself, last, because
//     install refuses while any BLOCK row stands.
//
// Inside a phase a BLOCK precedes a WARN, then the class's place in fixOrder,
// then an unknown class by name, then the detector's own row order.
type phase int

const (
	phaseConfig phase = iota
	phaseDatabase
	phaseStore
	phaseWiring
	phaseCleanup
	phaseUnknown
	phaseInstall
)

// phaseClasses names each phase's classes, space-separated; a class's
// position in this table is its rank inside its phase. store-identity
// follows account-is-store because its fix names that fix as its
// prerequisite. Every name here is a registered detector's check (a test
// pins it), so a renamed check cannot leave a stale entry behind.
var phaseClasses = map[phase]string{
	phaseConfig:   "legacy-config legacy-harvester-config pre-split-config",
	phaseDatabase: "legacy-state-db legacy-cache-db",
	phaseStore:    "account-is-store store-identity account-entry-real home-state-file",
	phaseWiring: "pfm-settings pfm-mcp memory-helpers staged-shim third-party-mcp " +
		"function-hook-modules shell-claude-env",
	phaseCleanup: "legacy-harvester-cache staged-prompts shared-db stray-dir unclassified " +
		"stale-state-tmp beside-backup",
	phaseInstall: "retired-store-entry",
}

type classPhase struct {
	class string
	phase phase
}

// fixOrder is phaseClasses in phase order, one entry per class.
var fixOrder = func() []classPhase {
	var order []classPhase
	for place := phaseConfig; place <= phaseInstall; place++ {
		for _, class := range strings.Fields(phaseClasses[place]) {
			order = append(order, classPhase{class, place})
		}
	}
	return order
}()

// failedProblems open the Problem of every row whose check could not look —
// unreadable's, unparsable's, and a check that declined to run — so the row
// is a failure, never a fix.
var failedProblems = []string{"UNREADABLE ", "cannot parse ", "not checked: "}

func failedCheck(row Row) bool {
	for _, prefix := range failedProblems {
		if strings.HasPrefix(row.Problem, prefix) {
			return true
		}
	}
	return false
}

// Plan is every host check's fix in the order to apply them. Ran counts the
// detectors that ran, so an empty plan reads "no fixes" only after they did.
type Plan struct {
	Ran    int
	Failed []Row
	Steps  []Row
}

// RunPlan runs every registered detector and orders its rows.
func RunPlan(env Env) Plan { return planFor(env, Detectors()) }

func planFor(env Env, detectors []Detector) Plan {
	return NewPlan(len(detectors), runDetectors(env, detectors))
}

// NewPlan splits rows into failed checks and fixes, the fixes in phase order.
func NewPlan(ran int, rows []Row) Plan {
	plan := Plan{Ran: ran}
	for _, row := range rows {
		if failedCheck(row) {
			plan.Failed = append(plan.Failed, row)
		} else {
			plan.Steps = append(plan.Steps, row)
		}
	}
	rank := make(map[string]int, len(fixOrder))
	phases := make(map[string]phase, len(fixOrder))
	for i, entry := range fixOrder {
		rank[entry.class], phases[entry.class] = i, entry.phase
	}
	key := func(row Row) (phase, int, int) {
		place, known := phases[row.Check]
		position := len(fixOrder)
		if known {
			position = rank[row.Check]
		} else {
			place = phaseUnknown
		}
		return place, severityOrder(row.Severity), position
	}
	sort.SliceStable(plan.Steps, func(i, j int) bool {
		a, b := plan.Steps[i], plan.Steps[j]
		aPhase, aSeverity, aRank := key(a)
		bPhase, bSeverity, bRank := key(b)
		switch {
		case aPhase != bPhase:
			return aPhase < bPhase
		case aSeverity != bSeverity:
			return aSeverity < bSeverity
		case aRank != bRank:
			return aRank < bRank
		default:
			return a.Check < b.Check
		}
	})
	return plan
}

func severityOrder(severity Severity) int {
	switch severity {
	case Block:
		return 0
	case Warn:
		return 1
	default:
		return 2
	}
}

// Blocking counts the BLOCK rows pfm install --yes would refuse on.
func (plan Plan) Blocking() int { return Count(plan.Failed, Block) + Count(plan.Steps, Block) }

// Print writes Render to w and returns pfm install's verdict on the plan:
// 1 when no check ran (the plan is unknown), 4 while a BLOCK row stands,
// else 0; a non-zero code comes with its refusal line.
func (plan Plan) Print(w io.Writer) (code int, refusal string) {
	if _, err := io.WriteString(w, plan.Render()); err != nil {
		return 1, "write the install plan: " + err.Error()
	}
	switch blocking := plan.Blocking(); {
	case plan.Ran == 0:
		return 1, "no host check ran; the plan is unknown"
	case blocking != 0:
		return 4, fmt.Sprintf(
			"%d blocking — apply the plan above in order, then rerun pfm install --check --plan",
			blocking,
		)
	}
	return 0, ""
}

// Render prints the plan: a header naming how many checks ran and failed,
// each failed check, each fix numbered in apply order with its fix line, and
// the step after it.
func (plan Plan) Render() string {
	if plan.Ran == 0 {
		return "install plan: FAILED — no host check ran; the plan is unknown\n"
	}
	var out strings.Builder
	fmt.Fprintf(&out, "install plan: %d host checks ran, %d failed — ", plan.Ran, len(plan.Failed))
	switch {
	case len(plan.Failed) != 0:
		out.WriteString("the plan is incomplete until every failed check can look\n")
	case len(plan.Steps) == 0:
		out.WriteString("no fixes\n")
		return out.String()
	default:
		fmt.Fprintf(&out, "%d fixes, apply them in this order\n", len(plan.Steps))
	}
	for _, row := range plan.Failed {
		fmt.Fprintf(&out, "  FAILED %s %s — %s\n      fix: %s\n", row.Check, row.Path, row.Problem, row.Fix)
	}
	for i, row := range plan.Steps {
		fmt.Fprintf(&out, "  %d. %s\n      fix: %s\n", i+1, row.Line(), row.Fix)
	}
	if len(plan.Failed) != 0 {
		out.WriteString("  then: rerun pfm install --check --plan\n")
	} else {
		out.WriteString("  then: pfm install --yes, then pfm doctor\n")
	}
	return out.String()
}
