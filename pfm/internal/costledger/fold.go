package costledger

import (
	"sort"
	"strings"
	"time"
)

// halfPairWindow is how far apart the two halves of one request-less call may be stamped (the log
// half's event time, the span half's end time). Measured read-only over every observation file of
// one host, 2026-11-18: of 154,181 pairs, 99.98% lie within 50 ms and every one within 1 s.
const halfPairWindow = time.Second

// halfKey is what both halves of one request-less call carry alike. The trace id is left out: a
// log carries its enclosing span's trace, which differs from the llm span's in 3.7% of the pairs.
type halfKey struct {
	querySource, model                  string
	input, output, cacheRead, cacheMade int64
}

type stampedHalf struct {
	index int
	at    time.Time
}

type halfPair struct {
	log, span int
	apart     time.Duration
}

// foldHalves joins each request-less call the sink wrote twice — a log half (the cost, agent "?")
// and a span half (the agent, no cost): Claude Code's request-less calls carry no id to join on
// when written — into one log+span row at the earlier half's place: the log half with the span
// half's agent and parent. Halves pair on query source, model (its "[1m]" window tag cut, which
// only the span names) and the four token counts, stamped within halfPairWindow, the nearest
// stamps first. A half with no partner stays as it is. Without an id nothing tells two alike calls
// inside the window apart: should one lose its span half and the other its log half, the two
// leftovers read as one priced call and that call's unknown cost is not counted.
func foldHalves(rows []Row) []Row {
	groups := map[halfKey]*[2][]stampedHalf{}
	for index := range rows {
		row := &rows[index]
		side := 0
		switch {
		case row.RequestID != "":
			continue
		case row.Join == JoinSpan:
			side = 1
		case row.Join != JoinLog:
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, row.TS)
		if err != nil {
			continue // a half without a readable stamp cannot be placed beside its partner; it stays alone
		}
		key := halfKey{
			querySource: row.QuerySource, model: strings.TrimSuffix(row.Model, "[1m]"),
			input: row.InputTokens, output: row.OutputTokens,
			cacheRead: row.CacheReadTokens, cacheMade: row.CacheCreationTokens,
		}
		group := groups[key]
		if group == nil {
			group = &[2][]stampedHalf{}
			groups[key] = group
		}
		group[side] = append(group[side], stampedHalf{index: index, at: at})
	}
	var candidates []halfPair
	for _, group := range groups {
		logs, spans := group[0], group[1]
		if len(logs) == 0 || len(spans) == 0 {
			continue
		}
		sort.Slice(spans, func(i, j int) bool { return spans[i].at.Before(spans[j].at) })
		for _, log := range logs {
			from := sort.Search(
				len(spans),
				func(i int) bool { return !spans[i].at.Before(log.at.Add(-halfPairWindow)) },
			)
			for _, span := range spans[from:] {
				if span.at.After(log.at.Add(halfPairWindow)) {
					break
				}
				candidates = append(
					candidates,
					halfPair{log: log.index, span: span.index, apart: log.at.Sub(span.at).Abs()},
				)
			}
		}
	}
	if len(candidates) == 0 {
		return rows
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.apart != b.apart {
			return a.apart < b.apart
		}
		if a.log != b.log {
			return a.log < b.log
		}
		return a.span < b.span
	})
	taken := make([]bool, len(rows))
	for _, pair := range candidates {
		if taken[pair.log] || taken[pair.span] {
			continue
		}
		taken[pair.log], taken[pair.span] = true, true
		joined := rows[pair.log]
		joined.AgentID, joined.ParentAgentID = rows[pair.span].AgentID, rows[pair.span].ParentAgentID
		joined.Join = JoinLogSpan
		place, gone := min(pair.log, pair.span), max(pair.log, pair.span)
		rows[place] = joined
		rows[gone].Join = foldedHalf
	}
	folded := rows[:0]
	for index := range rows {
		if rows[index].Join != foldedHalf {
			folded = append(folded, rows[index])
		}
	}
	return folded
}

// foldedHalf marks, inside foldHalves alone, the half whose row its partner's place now holds.
const foldedHalf = "\x00folded"
