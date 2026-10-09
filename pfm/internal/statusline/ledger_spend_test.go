package statusline

import (
	"errors"
	"math"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/costledger"
)

func ledgerRow(id, agent, parent string, cost int64) costledger.Row {
	return costledger.Row{
		RequestID: id, SessionID: spendSession, AgentID: agent, ParentAgentID: parent, InputTokens: 10,
		CacheReadTokens: 100, CacheCreationTokens: 1000, OutputTokens: 5, CostUSDMicros: micros(cost),
		Join: costledger.JoinLogSpan,
	}
}

// An agent's spend is its own rows and every agent's below it, the tree drawn by the rows'
// parent_agent_id and by the meta files, a cycle ending, an unattributed row ("?") never taken
// for a child, an agent with no row yet reported as none.
func TestLedgerViewSpendWalksTheLedgerAndMetaEdges(t *testing.T) {
	rows := []costledger.Row{
		ledgerRow("r1", "top", "", 1_000_000),
		ledgerRow("r2", "ledger-child", "top", 20_000),
		ledgerRow("r3", "ledger-grandchild", "ledger-child", 300),
		ledgerRow("r4", "meta-child", "", 4),
		ledgerRow("r5", costledger.UnknownAgent, "top", 50_000_000),
		ledgerRow("r6", "", "", 600_000_000),
	}
	view := newLedgerView(rows)
	meta := map[string][]string{"top": {"meta-child"}, "meta-child": {"top"}}
	cases := []struct {
		name   string
		id     string
		meta   map[string][]string
		micros int64
		rows   int
		known  bool
	}{
		{name: "ledger edges at any depth", id: "top", micros: 1_020_300, rows: 3, known: true},
		{name: "a child alone", id: "ledger-child", micros: 20_300, rows: 2, known: true},
		{
			name:   "meta edges join the ledger's, a cycle ends",
			id:     "top",
			meta:   meta,
			micros: 1_020_304,
			rows:   4,
			known:  true,
		},
		{name: "an agent with no row yet", id: "unwritten", known: false},
	}
	for _, tc := range cases {
		spend, ok := view.spend(tc.id, tc.meta)
		if ok != tc.known {
			t.Fatalf("%s: known = %v, want %v", tc.name, ok, tc.known)
		}
		if !ok {
			continue
		}
		wantIn := int64(tc.rows) * (10 + 100 + 1000)
		if math.Abs(spend.usd-float64(tc.micros)/1e6) > 1e-12 || spend.in != wantIn || spend.out != int64(tc.rows)*5 ||
			!spend.fromLedger || spend.partial {
			t.Errorf(
				"%s: spend = %+v, want $%v over %d rows (in %d)",
				tc.name,
				spend,
				float64(tc.micros)/1e6,
				tc.rows,
				wantIn,
			)
		}
	}
}

// A row whose cost never arrived leaves the agent's sum a floor, and the transcripts keep what the
// ledger does not record: tool calls and the advisor's own spend.
func TestLedgerSpendKeepsTranscriptToolsAndAdvisor(t *testing.T) {
	late := ledgerRow("r2", "top", "", 0)
	late.CostUSDMicros, late.Join = nil, costledger.JoinSpan
	booked := ledgerSpend([]costledger.Row{ledgerRow("r1", "top", "", 2_000_000), late})
	if !booked.partial || booked.usd != 2 || booked.priced != 1 {
		t.Fatalf("ledger spend = %+v, want a $2 floor", booked)
	}
	merged := agentSpend{
		usd:             99,
		in:              99,
		out:             99,
		tools:           7,
		advisorUSD:      0.5,
		advisorUnpriced: 1,
		priced:          3,
	}.withLedger(
		booked,
	)
	if merged.usd != 2 || merged.in != 2*1110 || merged.tools != 7 || merged.advisorUSD != 0.5 ||
		merged.advisorUnpriced != 1 || !merged.fromLedger {
		t.Fatalf("merged = %+v, want the ledger's dollars and tokens with the transcript's tools and advisor", merged)
	}
}

// The ledger total is the main thread's own rows plus every top-level agent with everything below
// it: nothing is counted twice and nothing is left out.
func TestLedgerSpendsAddUpToTheSessionTotal(t *testing.T) {
	ledgerDir := useLedgerSample(t)
	ledger, err := costledger.ForSession(ledgerDir, ledgerSampleSession)
	if err != nil || len(ledger.Rows) != 15 {
		t.Fatalf("sample ledger = %d rows, %v; want 15", len(ledger.Rows), err)
	}
	view := newLedgerView(ledger.Rows)
	nested := map[string]bool{}
	for _, children := range view.children {
		for _, child := range children {
			nested[child] = true
		}
	}
	rest := ledgerSpend(view.byAgent[""])
	roots := 0
	for agent := range view.byAgent {
		if agent == "" || nested[agent] {
			continue
		}
		roots++
		below, ok := view.spend(agent, nil)
		if !ok {
			t.Fatalf("top-level agent %s has rows but no spend", agent)
		}
		rest.usd += below.usd
		rest.in += below.in
		rest.out += below.out
	}
	all := ledgerSpend(ledger.Rows)
	if roots != 1 || math.Abs(rest.usd-all.usd) > 1e-12 || rest.in != all.in || rest.out != all.out {
		t.Fatalf("main + %d top-level = $%v/%d/%d, ledger total $%v/%d/%d", roots, rest.usd, rest.in, rest.out,
			all.usd, all.in, all.out)
	}
}

// What the launch recorded outranks the comparison, an unreadable ledger outranks Claude Code's
// total, and no marker is ever raised without a Claude Code total to compare.
func TestSpendMarkerPrecedence(t *testing.T) {
	present := costledger.Ledger{Present: true}
	cases := []struct {
		name       string
		books      ledgerBooks
		claude     float64
		ledgerUSD  float64
		loggedUSD  float64
		logged     bool
		wantMarker string
	}{
		{
			name: "sink failed beats a gap", books: ledgerBooks{ledger: present, marker: claudelaunch.CostLedgerFailed},
			claude: 10, ledgerUSD: 1, wantMarker: markerSinkFailed,
		},
		{
			name:       "preempted beats an unreadable ledger",
			books:      ledgerBooks{err: errors.New("x"), marker: claudelaunch.CostLedgerPreempted},
			claude:     10,
			wantMarker: markerPreempted,
		},
		{
			name:       "off with nothing to compare",
			books:      ledgerBooks{marker: claudelaunch.CostLedgerOff},
			wantMarker: markerLedgerOff,
		},
		{
			name: "unreadable beats a gap", books: ledgerBooks{err: errors.New("x"), marker: claudelaunch.CostLedgerOn},
			claude: 10, wantMarker: markerLedgerUnreadable,
		},
		{
			name: "a gap on a ledger", books: ledgerBooks{ledger: present, marker: claudelaunch.CostLedgerOn},
			claude: 10, ledgerUSD: 9, wantMarker: markerLedgerGap,
		},
		{
			name: "a ledger above claude code is a gap too", books: ledgerBooks{ledger: present},
			claude: 9, ledgerUSD: 10, wantMarker: markerLedgerGap,
		},
		{name: "no claude code total, nothing to compare", books: ledgerBooks{ledger: present}, ledgerUSD: 9},
		{
			name: "no ledger, transcripts below", books: ledgerBooks{}, claude: 10, loggedUSD: 9, logged: true,
			wantMarker: markerUnlogged,
		},
		{name: "no ledger, transcripts unpriced: nothing to say", books: ledgerBooks{}, claude: 10, loggedUSD: 0},
		{name: "no ledger, transcripts above claude code", books: ledgerBooks{}, claude: 5, loggedUSD: 9, logged: true},
	}
	for _, tc := range cases {
		if got := spendMarker(tc.books, tc.claude, tc.ledgerUSD, tc.loggedUSD, tc.logged); got != tc.wantMarker {
			t.Errorf("%s: marker %q, want %q", tc.name, got, tc.wantMarker)
		}
	}
}
