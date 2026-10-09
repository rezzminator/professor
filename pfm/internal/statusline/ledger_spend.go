package statusline

import (
	"context"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/costledger"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// The status line prices a session from the cost ledger (internal/costledger) when one exists:
// Claude Code's own price of every API call, the calls no transcript records included. The main
// line shows the ledger's total as its one dollar amount; a sub-agent row shows the ledger rows of
// its agent and every agent below it. A state the main line must flag carries one of these
// markers, none with an amount in it: the amounts behind them are `pfm tokens`'.
const (
	// markerSinkFailed: the cost sink could not be armed for this launch.
	markerSinkFailed = "sink failed"
	// markerPreempted: the user's own telemetry settings kept the ledger from starting.
	markerPreempted = "ledger preempted"
	// markerLedgerOff: claude.costLedger is false.
	markerLedgerOff = "ledger off"
	// markerUnlogged: no ledger, and the transcripts price below Claude Code's total, so calls
	// no transcript records exist.
	markerUnlogged = "unlogged"
	// markerLedgerUnreadable: the ledger file exists but could not be read.
	markerLedgerUnreadable = "ledger unreadable"

	// ledgerToleranceFloor and ledgerToleranceShare: the ledger and Claude Code agree when they
	// differ by at most the larger of one cent and half a percent of Claude Code's total.
	ledgerToleranceFloor = 0.01
	ledgerToleranceShare = 0.005
)

// ledgerBooks is what the main line learned about the session's cost ledger: the ledger itself
// (Present false when none exists), the error that kept a present one from being read, and the
// launch's own record of what the ledger decided (claudelaunch.EnvCostLedger, "" outside a pfm
// launch).
type ledgerBooks struct {
	ledger costledger.Ledger
	err    error
	marker string
}

func (runtime Runtime) pathsEnv() paths.Env {
	if runtime.Env != nil {
		return &paths.MapEnv{Values: runtime.Env}
	}
	return paths.OSEnv{}
}

// readLedgerBooks reads the session's ledger. A payload that names no session has no ledger; any
// other failure to read one is kept in err and logged, never read as "no ledger".
func readLedgerBooks(runtime Runtime, sessionID string) ledgerBooks {
	books := ledgerBooks{marker: runtime.getenv(claudelaunch.EnvCostLedger)}
	if strings.TrimSpace(sessionID) == "" {
		return books
	}
	dir := paths.CostLedgerDir(runtime.pathsEnv(), runtime.Home)
	ledger, err := costledger.ForSession(dir, sessionID)
	if err != nil {
		obs.Logger(context.Background()).Warn(
			"statusline: the cost ledger could not be read; the line falls back to Claude Code's total",
			"dir", dir, "session", sessionID, obs.FieldErr, err.Error(),
		)
		books.err = err
		return books
	}
	if ledger.Faults > 0 {
		obs.Logger(context.Background()).Warn(
			"statusline: the cost ledger holds lines that are not rows; they are left out of its total",
			"dir", dir, "session", sessionID, "lines", ledger.Faults,
		)
	}
	books.ledger = ledger
	return books
}

// ledgerTolerance is how far the ledger and Claude Code may differ before the line says so.
func ledgerTolerance(claudeTotal float64) float64 {
	return math.Max(ledgerToleranceFloor, ledgerToleranceShare*claudeTotal)
}

// spendMarker is the one amount-free flag the main line carries, "" when nothing needs one. What
// the launch recorded comes first: a launch whose ledger failed, was pre-empted or is off is not
// logging, whatever file a previous launch of the session left. Then a ledger that could not be
// read; then, with no ledger, the transcripts' logged price against Claude Code's total (loggedUSD,
// valid when logged). The ledger is never compared with Claude Code's total on the line: `pfm
// tokens` names that difference.
func spendMarker(books ledgerBooks, claudeTotal, loggedUSD float64, logged bool) string {
	switch books.marker {
	case claudelaunch.CostLedgerFailed:
		return markerSinkFailed
	case claudelaunch.CostLedgerPreempted:
		return markerPreempted
	case claudelaunch.CostLedgerOff:
		return markerLedgerOff
	}
	tolerance := ledgerTolerance(claudeTotal)
	switch {
	case books.err != nil:
		return markerLedgerUnreadable
	case claudeTotal <= 0:
		return ""
	case !books.ledger.Present && logged && claudeTotal-loggedUSD > tolerance:
		return markerUnlogged
	}
	return ""
}

// ledgerSpend is what rows billed: Claude Code's own price, every prompt token (uncached, cache
// read, cache write) and every output token. A row whose cost never arrived leaves the sum a
// floor.
func ledgerSpend(rows []costledger.Row) agentSpend {
	sum := costledger.Sum(rows)
	return agentSpend{
		usd:        float64(sum.Micros) / 1e6,
		in:         sum.InputTokens + sum.CacheReadTokens + sum.CacheCreationTokens,
		out:        sum.OutputTokens,
		priced:     1,
		partial:    sum.UnknownCost > 0,
		fromLedger: true,
	}
}

// withLedger is spend with its dollars and tokens taken from the ledger's. Tool calls and the
// advisor's own spend are the transcripts' alone: the ledger records neither.
func (spend agentSpend) withLedger(ledger agentSpend) agentSpend {
	ledger.tools = spend.tools
	ledger.advisorUSD = spend.advisorUSD
	ledger.advisorUnpriced = spend.advisorUnpriced
	return ledger
}

// ledgerView is one session's ledger indexed for the sub-agent rows: the rows of each agent and
// the agent tree its parent_agent_id values draw.
type ledgerView struct {
	byAgent  map[string][]costledger.Row
	children map[string][]string
}

func newLedgerView(rows []costledger.Row) *ledgerView {
	view := &ledgerView{byAgent: map[string][]costledger.Row{}, children: map[string][]string{}}
	edges := map[[2]string]bool{}
	for index := range rows {
		row := &rows[index]
		view.byAgent[row.AgentID] = append(view.byAgent[row.AgentID], *row)
		parent := row.ParentAgentID
		if row.AgentID == "" || row.AgentID == costledger.UnknownAgent || parent == "" || parent == row.AgentID ||
			edges[[2]string{parent, row.AgentID}] {
			continue
		}
		edges[[2]string{parent, row.AgentID}] = true
		view.children[parent] = append(view.children[parent], row.AgentID)
	}
	return view
}

// spend is what agent id and every agent below it billed, at any depth, the tree being the ledger's
// edges joined with the meta files' (metaChildren: parent to children). False when the ledger
// holds no row of any of them yet: its calls reach the ledger up to 20 s late.
func (view *ledgerView) spend(id string, metaChildren map[string][]string) (agentSpend, bool) {
	var rows []costledger.Row
	seen := map[string]bool{}
	queue := []string{id}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		if seen[next] {
			continue
		}
		seen[next] = true
		rows = append(rows, view.byAgent[next]...)
		queue = append(queue, view.children[next]...)
		queue = append(queue, metaChildren[next]...)
	}
	if len(rows) == 0 {
		return agentSpend{}, false
	}
	return ledgerSpend(rows), true
}

// loadLedgerView reads the session's ledger for the sub-agent rows: nil when the payload names no
// session or the session has no ledger. A ledger that cannot be read is told on warn and leaves
// the rows on their transcripts' pricing.
func loadLedgerView(sessionID string, warn io.Writer) *ledgerView {
	if strings.TrimSpace(sessionID) == "" {
		return nil
	}
	home, err := paths.Home()
	if err != nil {
		fmt.Fprintf(warn, "pfm statusline --subagents: cost ledger (rows price from transcripts): %v\n", err)
		return nil
	}
	ledger, err := costledger.ForSession(paths.CostLedgerDir(paths.OSEnv{}, home), sessionID)
	if err != nil {
		fmt.Fprintf(warn, "pfm statusline --subagents: cost ledger (rows price from transcripts): %v\n", err)
		return nil
	}
	if !ledger.Present {
		return nil
	}
	return newLedgerView(ledger.Rows)
}
