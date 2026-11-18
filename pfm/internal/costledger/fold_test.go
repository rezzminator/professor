package costledger

import "testing"

const (
	foldTrace     = "7a87346f49bf253d079d966a1e591680"
	foldOtherSpan = "df3977383deb1bcb"
)

// half is one request-less row as the sink writes it: a log half carries the cost and agent "?",
// a span half the agent and no cost.
func half(join, ts, agent, model string, in, out int64, cost *int64, spanID string) Row {
	return Row{
		TS: ts, SessionID: sessionA, AgentID: agent, QuerySource: "agent_summary", Model: model,
		InputTokens: in, OutputTokens: out, CacheReadTokens: 300, CacheCreationTokens: 40,
		CostUSDMicros: cost, Speed: "normal", Effort: "high", Join: join, TraceID: foldTrace, SpanID: spanID,
	}
}

// Each request-less call the sink wrote twice, a log half and a span half, reads as one row: the
// log half's cost, the span half's agent, its tokens once. A half with no partner reads as it is.
func TestReadFoldsTheTwoHalvesOfARequestlessCall(t *testing.T) {
	logHalf := half(
		JoinLog,
		"2026-11-01T05:14:48.841Z",
		UnknownAgent,
		"claude-opus-5-5",
		10,
		2,
		micros(40),
		"3bf5c6d39d319461",
	)
	spanHalf := half(
		JoinSpan,
		"2026-11-01T05:14:48.840Z",
		"af5ff631c56f3db94",
		"claude-opus-5-5[1m]",
		10,
		2,
		nil,
		"81196dc5ba4e3c35",
	)
	spanHalf.TraceID = "2e26ff246250f68367dda115c44ee9f5"
	spanAlone := half(
		JoinSpan,
		"2026-11-01T05:15:18.807Z",
		"a05a97f21afaf0c6d",
		"claude-opus-5-5",
		7,
		5,
		nil,
		"4c73ad4109de2aaa",
	)
	logAlone := half(
		JoinLog,
		"2026-11-01T05:15:48.000Z",
		UnknownAgent,
		"claude-opus-5-5",
		3,
		9,
		micros(60),
		"08348527ca9691dc",
	)
	at := func(r Row, ts, spanID string) Row { r.TS, r.SpanID = ts, spanID; return r }
	agent := func(r Row, id string) Row { r.AgentID = id; return r }
	type kept struct {
		agent, join string
		cost        int64 // -1: null
	}
	for _, tc := range []struct {
		name  string
		rows  []Row
		kept  []kept
		total Totals
	}{
		{
			name:  "a log half and its span half are one priced call of the span's agent",
			rows:  []Row{logHalf, spanHalf},
			kept:  []kept{{"af5ff631c56f3db94", JoinLogSpan, 40}},
			total: Totals{Micros: 40, Rows: 1, InputTokens: 10, OutputTokens: 2, CacheReadTokens: 300, CacheCreationTokens: 40},
		},
		{
			name:  "a span half alone is an unknown cost",
			rows:  []Row{spanAlone},
			kept:  []kept{{"a05a97f21afaf0c6d", JoinSpan, -1}},
			total: Totals{UnknownCost: 1, Rows: 1, InputTokens: 7, OutputTokens: 5, CacheReadTokens: 300, CacheCreationTokens: 40},
		},
		{
			name:  "a log half alone counts as it is",
			rows:  []Row{logAlone},
			kept:  []kept{{UnknownAgent, JoinLog, 60}},
			total: Totals{Micros: 60, Rows: 1, InputTokens: 3, OutputTokens: 9, CacheReadTokens: 300, CacheCreationTokens: 40},
		},
		{
			name: "a pair, a span alone and a log alone: the pair at its first half's place",
			rows: []Row{logHalf, spanAlone, spanHalf, logAlone},
			kept: []kept{{"af5ff631c56f3db94", JoinLogSpan, 40}, {"a05a97f21afaf0c6d", JoinSpan, -1}, {UnknownAgent, JoinLog, 60}},
			total: Totals{
				Micros: 100, UnknownCost: 1, Rows: 3, InputTokens: 20, OutputTokens: 16, CacheReadTokens: 900,
				CacheCreationTokens: 120,
			},
		},
		{
			name:  "halves a second apart still pair",
			rows:  []Row{at(logHalf, "2026-11-01T05:14:49.840Z", "3bf5c6d39d319461"), spanHalf},
			kept:  []kept{{"af5ff631c56f3db94", JoinLogSpan, 40}},
			total: Totals{Micros: 40, Rows: 1, InputTokens: 10, OutputTokens: 2, CacheReadTokens: 300, CacheCreationTokens: 40},
		},
		{
			name: "halves more than a second apart are two calls",
			rows: []Row{at(logHalf, "2026-11-01T05:14:49.841Z", "3bf5c6d39d319461"), spanHalf},
			kept: []kept{{UnknownAgent, JoinLog, 40}, {"af5ff631c56f3db94", JoinSpan, -1}},
			total: Totals{
				Micros: 40, UnknownCost: 1, Rows: 2, InputTokens: 20, OutputTokens: 4, CacheReadTokens: 600,
				CacheCreationTokens: 80,
			},
		},
		{
			name: "two alike calls pair by the nearest stamps, the main thread's span naming no agent",
			rows: []Row{
				logHalf, at(logHalf, "2026-11-01T05:14:49.341Z", "1bf5c6d39d319461"),
				at(agent(spanHalf, ""), "2026-11-01T05:14:49.340Z", foldOtherSpan), spanHalf,
			},
			kept:  []kept{{"af5ff631c56f3db94", JoinLogSpan, 40}, {"", JoinLogSpan, 40}},
			total: Totals{Micros: 80, Rows: 2, InputTokens: 20, OutputTokens: 4, CacheReadTokens: 600, CacheCreationTokens: 80},
		},
		{
			name:  "a log that lost its span and an alike span that lost its log read as one priced call",
			rows:  []Row{logHalf, at(agent(spanHalf, "a05a97f21afaf0c6d"), "2026-11-01T05:14:49.841Z", foldOtherSpan)},
			kept:  []kept{{"a05a97f21afaf0c6d", JoinLogSpan, 40}},
			total: Totals{Micros: 40, Rows: 1, InputTokens: 10, OutputTokens: 2, CacheReadTokens: 300, CacheCreationTokens: 40},
		},
		{
			name: "halves of different token counts are two calls",
			rows: []Row{logHalf, func() Row { r := spanHalf; r.OutputTokens = 3; return r }()},
			kept: []kept{{UnknownAgent, JoinLog, 40}, {"af5ff631c56f3db94", JoinSpan, -1}},
			total: Totals{
				Micros: 40, UnknownCost: 1, Rows: 2, InputTokens: 20, OutputTokens: 5, CacheReadTokens: 600,
				CacheCreationTokens: 80,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, r := range tc.rows {
				if err := Append(dir, r); err != nil {
					t.Fatal(err)
				}
			}
			ledger, err := ForSession(dir, sessionA)
			if err != nil || ledger.Faults != 0 {
				t.Fatalf("ForSession = %+v, %v", ledger, err)
			}
			var got []kept
			for _, r := range ledger.Rows {
				cost := int64(-1)
				if r.CostUSDMicros != nil {
					cost = *r.CostUSDMicros
				}
				got = append(got, kept{r.AgentID, r.Join, cost})
			}
			if len(got) != len(tc.kept) {
				t.Fatalf("rows = %v, want %v", got, tc.kept)
			}
			for i := range tc.kept {
				if got[i] != tc.kept[i] {
					t.Fatalf("rows = %v, want %v", got, tc.kept)
				}
			}
			if total := Sum(ledger.Rows); total != tc.total {
				t.Fatalf("Sum = %+v, want %+v", total, tc.total)
			}
		})
	}
}
