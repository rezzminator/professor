package statusline

import (
	"context"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

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
	// markerPreLedger: the session spent money before its ledger began (it started, or ran a launch,
	// without a sink); the one dollar amount holds it, no ledger row does.
	markerPreLedger = "pre-ledger"

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

// preLedgerUSD is what the session spent before its ledger began, in dollars: what the cost-state
// lines of its transcript (Claude Code's own totals at each exit) hold beyond the ledger's rows up
// to that exit (costledger.BeforeLedger).
func preLedgerUSD(rows []costledger.Row, spend agentSpend) float64 {
	return float64(costledger.BeforeLedger(rows, spend.costStates)) / 1e6
}

// completeSpend is the session's one dollar amount when a ledger exists: the ledger's total plus
// what came before it. Claude Code's own total leads only when it is ahead of that by more than
// the tolerance (a call whose row has not reached the ledger yet, a launch that logged nothing);
// within it the ledger's figure stands, so the main line adds up with the sub-agent rows and
// `pfm tokens` to the cent.
func completeSpend(ledgerUSD, beforeUSD, claudeTotal float64) float64 {
	complete := ledgerUSD + beforeUSD
	if claudeTotal-complete > ledgerTolerance(claudeTotal) {
		return claudeTotal
	}
	return complete
}

// spendMarker is the one amount-free flag the main line carries, "" when nothing needs one. What
// the launch recorded comes first: a launch whose ledger failed, was pre-empted or is off is not
// logging, whatever file a previous launch of the session left. Then a ledger that could not be
// read; then spend that precedes the ledger (beforeUSD, a fact of the files: it does not move with
// a call in flight); then, with no ledger, the transcripts' logged price against Claude Code's
// total (loggedUSD, valid when logged). The ledger is never compared with Claude Code's total on
// the line: `pfm tokens` names that difference.
func spendMarker(books ledgerBooks, claudeTotal, loggedUSD float64, logged bool, beforeUSD float64) string {
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
	case beforeUSD > tolerance:
		return markerPreLedger
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
	spend := agentSpend{
		usd:        float64(sum.Micros) / 1e6,
		in:         sum.InputTokens + sum.CacheReadTokens + sum.CacheCreationTokens,
		out:        sum.OutputTokens,
		priced:     1,
		partial:    sum.UnknownCost > 0,
		fromLedger: true,
	}
	for index := range rows {
		row := &rows[index]
		if !gatewayModel(row.Model) {
			continue
		}
		usd := 0.0
		if row.CostUSDMicros != nil {
			usd = float64(*row.CostUSDMicros) / 1e6
		}
		spend.gateway.add(
			gatewaySpend{
				present: true,
				usd:     usd,
				in:      row.InputTokens + row.CacheReadTokens + row.CacheCreationTokens,
				out:     row.OutputTokens,
				partial: row.CostUSDMicros == nil,
			},
		)
	}
	return spend
}

// withLedger preserves native dollars and supplements missing gateway API-equivalent
// spend. Tool calls and advisor spend come only from the transcripts.
func (spend agentSpend) withLedger(ledger agentSpend) agentSpend {
	// Native charges remain authoritative; a subscription-backed gateway row
	// may be absent or explicitly zero. Supplement only its uncovered subtotal.
	ledger.usd += max(0, spend.gateway.usd-ledger.gateway.usd)
	ledger.in += max(int64(0), spend.gateway.in-ledger.gateway.in)
	ledger.out += max(int64(0), spend.gateway.out-ledger.gateway.out)
	ledger.partial = ledger.partial || spend.gateway.partial
	ledger.gateway.present = ledger.gateway.present || spend.gateway.present
	ledger.gateway.usd = max(ledger.gateway.usd, spend.gateway.usd)
	ledger.gateway.in = max(ledger.gateway.in, spend.gateway.in)
	ledger.gateway.out = max(ledger.gateway.out, spend.gateway.out)
	ledger.gateway.partial = ledger.gateway.partial || spend.gateway.partial
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

// loadLedgerView reads the session's ledger for the sub-agent rows, under the home and env the
// render is handed (the main line's readLedgerBooks reads the same one through its Runtime): nil
// when the payload names no session or the session has no ledger. A ledger that cannot be read is told on warn and leaves
// the rows on their transcripts' pricing.
func loadLedgerView(sessionID, home string, env paths.Env, warn io.Writer) *ledgerView {
	if strings.TrimSpace(sessionID) == "" {
		return nil
	}
	ledger, err := costledger.ForSession(paths.CostLedgerDir(env, home), sessionID)
	if err != nil {
		fmt.Fprintf(warn, "pfm statusline --subagents: cost ledger (rows price from transcripts): %v\n", err)
		return nil
	}
	if !ledger.Present {
		return nil
	}
	return newLedgerView(ledger.Rows)
}

// gatewaySpend is the transcript's API-equivalent subtotal, not subscription
// dollars charged. It stays separate so native telemetry cannot erase it.
type gatewayCall struct {
	at  time.Time
	usd float64
}

type gatewaySpend struct {
	calls   []gatewayCall
	present bool
	usd     float64
	in, out int64
	partial bool
}

func (g *gatewaySpend) add(other gatewaySpend) {
	g.calls = append(g.calls, other.calls...)
	g.present = g.present || other.present
	g.usd += other.usd
	g.in += other.in
	g.out += other.out
	g.partial = g.partial || other.partial
}

// beforeGatewayOverlap preserves each native exit's remainder. Only calls
// that could precede that exit may overlap it; later API-equivalent calls are
// additive. Historical overlap is unknowable and must leave an explicit floor.
func beforeGatewayOverlap(rows []costledger.Row, spend agentSpend, supplement float64) (float64, bool) {
	before := 0.0
	ambiguous := false
	for _, state := range spend.costStates {
		remainder := float64(costledger.BeforeLedger(rows, []costledger.CostState{state})) / 1e6
		possible := 0.0
		for _, call := range spend.gateway.calls {
			// Until is the next launch's first stamped line, not part of this exit.
			later := !call.at.IsZero() && ((!state.Until.IsZero() && !call.at.Before(state.Until)) ||
				(state.Until.IsZero() && !state.At.IsZero() && call.at.After(state.At)))
			if !later {
				possible += call.usd
			}
		}
		overlap := min(remainder, supplement, possible)
		ambiguous = ambiguous || overlap > 0
		before = max(before, remainder-overlap)
	}
	return before, ambiguous
}

func (g gatewaySpend) note() string {
	if !g.present {
		return ""
	}
	return cMuted + " API-eq" + reset
}
